package app

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type connectorCompletion struct{ User, Provider, Generation string }

func (s *Server) publishConnector(ctx context.Context, tx *sql.Tx, completion *connectorCompletion) error {
	result, err := tx.ExecContext(ctx, `UPDATE cc_connector_accounts SET last_sync=now(),updated_at=now() WHERE user_id=$1 AND provider=$2 AND generation=$3 AND credentials<>''`, completion.User, completion.Provider, completion.Generation)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return fail(409, "The connector was disconnected before the sync finished")
	}
	return nil
}

func (s *Server) connectorImportAllowed(ctx context.Context, user string, d connectorDefinition, generation string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	u, err := s.loadUser(ctx, user)
	if err != nil {
		return fail(403, "The account is no longer available")
	}
	if err = connectorPermit(u, d); err != nil {
		return err
	}
	v, err := s.connectorConfig(ctx, d.Settings)
	if err != nil {
		return err
	}
	if !s.connectorConfigured(d, v) {
		return fail(409, "The connector is no longer configured")
	}
	var allowed bool
	err = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cc_connector_accounts WHERE user_id=$1 AND provider=$2 AND generation=$3 AND credentials<>'')`, user, d.ID, generation).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return fail(409, "The connector was disconnected or reconnected")
	}
	if lease, ok := ctx.Value(jobLeaseKey{}).(jobLease); ok {
		if err = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cc_jobs WHERE id=$1 AND lease_token=$2 AND status='running' AND lease_until>now())`, lease.ID, lease.Token).Scan(&allowed); err != nil {
			return err
		}
		if !allowed {
			return context.Canceled
		}
	}
	return nil
}
func connectorDocumentID(user, provider, id string, data []byte) string {
	return digest(string(jsonBytes([]string{user, provider, id, digest(string(data))})))[:32]
}
func (s *Server) discardConnectorImport(user, id string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), 5*time.Second)
	defer cancel()
	doc, err := s.getObject(ctx, user, "document", id)
	if err != nil {
		return
	}
	for _, key := range []string{"memory_id", "index_memory_id"} {
		if memory := str(doc, key); memory != "" {
			_ = s.scheduleMemoryDeletion(ctx, memory)
		}
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM cc_objects WHERE kind='document' AND id=$1 AND owner_id=$2`, id, user)
}
func (s *Server) importConnectorFile(ctx context.Context, user string, d connectorDefinition, v map[string]string, generation string, file connectorFile) (bool, error) {
	if file.Size > s.cfg.MaxUpload {
		return false, bad("File exceeds the upload limit")
	}
	if file.Modified != "" {
		var unchanged bool
		err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cc_objects WHERE kind='document' AND owner_id=$1 AND data->>'source'=$2 AND data->>'source_id'=$3 AND data->>'status'='indexed' AND data->'metadata'->>'source_modified_at'=$4 AND data->'metadata'->>'source_size'=$5)`, user, d.ID, file.ID, file.Modified, fmt.Sprint(file.Size)).Scan(&unchanged)
		if err != nil {
			return false, err
		}
		if unchanged {
			return true, nil
		}
	}
	data, err := s.connectorFileBytes(ctx, user, d, v, generation, file)
	if err != nil {
		return false, err
	}
	if err = s.connectorImportAllowed(ctx, user, d, generation); err != nil {
		return false, err
	}
	select {
	case s.uploads <- struct{}{}:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	metadata := M{}
	for k, v := range file.Metadata {
		metadata[k] = v
	}
	metadata["source_modified_at"] = nullable(file.Modified)
	metadata["source_size"] = file.Size
	id := connectorDocumentID(user, d.ID, file.ID, data)
	doc, created, err := s.beginDocumentIngest(ctx, user, "", id, file.Name, d.ID, file.ID, data, metadata)
	<-s.uploads
	if guard := s.connectorImportAllowed(ctx, user, d, generation); guard != nil {
		if created {
			s.discardConnectorImport(user, id)
		}
		return false, guard
	}
	if err != nil {
		return false, err
	}
	defer func() {
		if created && ctx.Err() != nil {
			s.discardConnectorImport(user, id)
		}
	}()
	if err = s.connectorImportAllowed(ctx, user, d, generation); err != nil {
		if created {
			s.discardConnectorImport(user, id)
		}
		return false, err
	}
	if str(doc, "status") != "indexed" {
		if _, err = s.waitDocumentIndexed(ctx, user, id); err != nil {
			return false, err
		}
	}
	if err = s.connectorImportAllowed(ctx, user, d, generation); err != nil {
		if created {
			s.discardConnectorImport(user, id)
		}
		return false, err
	}
	if !created {
		_, err = s.db.ExecContext(ctx, `UPDATE cc_objects SET data=jsonb_set(data,'{metadata}',(data->'metadata') || $3::jsonb) WHERE kind='document' AND id=$1 AND owner_id=$2`, id, user, jsonBytes(metadata))
	}
	return !created, err
}

func (s *Server) executeConnectorSync(ctx context.Context, user string, in M) (result M, err error) {
	d, err := s.connectorDefinition(str(in, "provider"))
	if err != nil {
		return nil, err
	}
	generation := str(in, "generation")
	if err = s.connectorImportAllowed(ctx, user, d, generation); err != nil {
		return nil, err
	}
	v, err := s.connectorConfig(ctx, d.Settings)
	if err != nil {
		return nil, err
	}
	processed, skipped, total, errorCount, consecutive := 0, 0, 0, 0, 0
	errors := []M{}
	record := func(name, reason string) {
		errorCount++
		if len(errors) < 20 {
			errors = append(errors, M{"name": name, "reason": reason})
		}
	}
	defer func() {
		if err != nil {
			audit, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			s.audit(audit, user, "connector.sync.failure", "connector", d.ID, M{"docs_processed": processed, "errors_count": errorCount, "reason": connectorSafeError(err)})
		}
	}()
	type folderScope struct {
		ID, Name string
		Depth    int
	}
	folders := []folderScope{{str(in, "folder_id"), fallback(str(in, "folder_id"), d.Label), 0}}
	seenFolders, seenFiles := map[string]bool{}, map[string]bool{}
	for len(folders) > 0 {
		folder := folders[0]
		folders = folders[1:]
		if seenFolders[folder.ID] {
			continue
		}
		seenFolders[folder.ID] = true
		if folder.Depth > 128 {
			record(folder.Name, "Folder nesting exceeds 128 levels")
			continue
		}
		cursor := ""
		seenPages := map[string]bool{}
		for {
			if err = s.connectorImportAllowed(ctx, user, d, generation); err != nil {
				return nil, err
			}
			if seenPages[cursor] {
				record(folder.Name, "Repeated page token stopped to avoid a crawl loop")
				break
			}
			seenPages[cursor] = true
			s.jobProgress(ctx, str(in, "job_id"), fmt.Sprintf("Reading %s; %d indexed, %d unchanged…", folder.Name, processed, skipped), 0)
			page, e := s.connectorList(ctx, user, d, v, generation, folder.ID, cursor)
			if e != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if folder.Depth == 0 {
					return nil, fail(502, connectorSafeError(e))
				}
				record(folder.Name+"/", connectorSafeError(e))
				break
			}
			for _, issue := range page.Errors {
				record(str(issue, "name"), str(issue, "reason"))
			}
			for _, file := range page.Files {
				if file.MIME == "folder" {
					if !seenFolders[file.ID] {
						folders = append(folders, folderScope{file.ID, file.Name, folder.Depth + 1})
					}
					continue
				}
				if seenFiles[file.ID] {
					continue
				}
				seenFiles[file.ID] = true
				// One allowlist across uploads, pickers and syncs. Validation of
				// actual bytes still happens after the capped download.
				if !connectorSupportedFile(file.Name) {
					continue
				}
				total++
				same, e := s.importConnectorFile(ctx, user, d, v, generation, file)
				if e != nil {
					if err = s.connectorImportAllowed(ctx, user, d, generation); err != nil {
						return nil, err
					}
					record(file.Name, connectorSafeError(e))
					consecutive++
					if consecutive >= 10 {
						return nil, fail(502, "Sync stopped after 10 consecutive file failures. Check the connector grant and provider status.")
					}
					continue
				}
				consecutive = 0
				if same {
					skipped++
				} else {
					processed++
				}
			}
			if page.Next == "" {
				break
			}
			cursor = page.Next
		}
	}
	if err = s.connectorImportAllowed(ctx, user, d, generation); err != nil {
		return nil, err
	}
	outputs, ok := ctx.Value(jobOutputsKey{}).(*jobOutputs)
	if !ok {
		return nil, fail(500, "Connector sync requires a durable job")
	}
	outputs.Lock()
	outputs.Connector = &connectorCompletion{user, d.ID, generation}
	outputs.Unlock()
	result = M{"status": "completed", "docs_processed": processed, "docs_skipped": skipped, "docs_total": total, "errors_count": errorCount, "errors": errors}
	s.jobAudit(ctx, user, "connector.sync.complete", "connector", d.ID, result)
	return result, nil
}
func connectorSupportedFile(name string) bool {
	return slices.Contains([]string{".pdf", ".docx", ".xlsx", ".pptx", ".odt", ".txt", ".md", ".csv", ".tsv", ".html", ".htm", ".json", ".eml", ".rtf", ".doc", ".xls", ".ppt", ".msg"}, strings.ToLower(filepath.Ext(name)))
}
