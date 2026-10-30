package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

func (s *Server) maintenanceAllowed(ctx context.Context, user, permission string) error {
	u, err := s.loadUser(ctx, user)
	if err != nil {
		return fail(403, "The account is no longer available")
	}
	if u.MustChangePassword || u.MFAEnrollmentRequired || !slices.Contains(u.Permissions, permission) {
		return fail(403, "Your account no longer has permission for this operation")
	}
	if lease, ok := ctx.Value(jobLeaseKey{}).(jobLease); ok {
		var valid bool
		if err = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cc_jobs WHERE id=$1 AND user_id=$2 AND lease_token=$3 AND status='running' AND lease_until>now())`, lease.ID, user, lease.Token).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return context.Canceled
		}
	}
	return ctx.Err()
}

// Snapshot only the caller's documents, including failed ones. Matter membership
// grants reading, not permission to rebuild or clear a colleague's library.
func ownedDocumentIDs(ctx context.Context, db *sql.DB, user string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM cc_objects WHERE kind='document' AND owner_id=$1 ORDER BY created_at,id`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Server) reindexDocuments(q request) (any, error) {
	if err := permit(q, "documents.upload"); err != nil {
		return nil, err
	}
	ctx := q.r.Context()
	// A second click/reconnected browser attaches to the existing rebuild. Keep
	// the same per-user lock and quota as every other durable job submission.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,1))`, q.user.ID); err != nil {
		return nil, err
	}
	var id string
	err = tx.QueryRowContext(ctx, `SELECT id FROM cc_jobs WHERE user_id=$1 AND kind='document.reindex' AND status IN ('pending','running') ORDER BY created_at LIMIT 1`, q.user.ID).Scan(&id)
	if err != nil && !notFound(err) {
		return nil, err
	}
	if id == "" {
		var queued int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM cc_jobs WHERE user_id=$1 AND kind<>'document.ocr_index' AND status IN ('pending','running')`, q.user.ID).Scan(&queued); err != nil {
			return nil, err
		}
		if queued >= 5 {
			return nil, fail(429, "Five jobs are already in progress. Wait for one to finish.")
		}
		var raw []byte
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(id ORDER BY created_at,id),'[]'::jsonb) FROM cc_objects WHERE kind='document' AND owner_id=$1`, q.user.ID).Scan(&raw); err != nil {
			return nil, err
		}
		var ids []string
		if err = json.Unmarshal(raw, &ids); err != nil {
			return nil, err
		}
		id = uuid()
		input := M{"reindex_document_ids": ids, "job_id": id}
		if _, err = tx.ExecContext(ctx, `INSERT INTO cc_jobs(id,user_id,kind,input) VALUES($1,$2,'document.reindex',$3)`, id, q.user.ID, jsonBytes(input)); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.audit(ctx, q.user.ID, "settings.update", "index", id, M{"action": "reindex_started"})
	if q.r.URL.Query().Get("async") == "true" {
		return M{"status": "pending", "job_id": id}, nil
	}
	for {
		var state string
		var raw []byte
		var message sql.NullString
		if err = s.db.QueryRowContext(ctx, `SELECT status,result,error FROM cc_jobs WHERE id=$1 AND user_id=$2`, id, q.user.ID).Scan(&state, &raw, &message); err != nil {
			return nil, err
		}
		switch state {
		case "completed":
			var out M
			err = json.Unmarshal(raw, &out)
			return out, err
		case "failed", "cancelled":
			return nil, fail(502, fallback(message.String, "The rebuild did not finish. Completed documents retain their new index."))
		}
		if err = connectorWait(ctx, 500*time.Millisecond); err != nil {
			return nil, err
		}
	}
}

func (s *Server) indexModel(ctx context.Context) string {
	space, err := s.gm.JSON(ctx, "GET", "/v1/spaces/"+url.PathEscape(s.gm.Space), nil)
	if err != nil {
		return "GoodMem"
	}
	models := []string{}
	for _, entry := range maps(space["spaceEmbedders"]) {
		id := str(entry, "embedderId")
		if id == "" {
			continue
		}
		model, e := s.gm.JSON(ctx, "GET", "/v1/embedders/"+url.PathEscape(id), nil)
		if e != nil {
			continue
		}
		if name := str(model, "modelIdentifier"); name != "" {
			models = append(models, name)
		}
	}
	if len(models) == 0 {
		return "GoodMem"
	}
	return strings.Join(models, ", ")
}

func reindexSummary(count int, failures []string, model string) M {
	if failures == nil {
		failures = []string{}
	}
	return M{"status": "completed", "reindexed": count, "errors": failures, "embedding_model": model, "message": fmt.Sprintf("Reindexed %d documents with %s", count, model)}
}

func (s *Server) executeReindex(ctx context.Context, user string, input M) (M, error) {
	if err := s.maintenanceAllowed(ctx, user, "documents.upload"); err != nil {
		return nil, err
	}
	modelCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	model := s.indexModel(modelCtx)
	cancel()
	ids := stringsOf(input["reindex_document_ids"])
	count := 0
	failures := []string{}
	for i, id := range ids {
		if err := s.maintenanceAllowed(ctx, user, "documents.upload"); err != nil {
			return nil, err
		}
		doc, err := s.getObject(ctx, user, "document", id)
		name := fallback(str(doc, "filename"), id)
		if err == nil && str(doc, "user_id") != user {
			err = missing()
		}
		if err == nil {
			s.jobProgress(ctx, str(input, "job_id"), fmt.Sprintf("Rebuilding %s (%d of %d)…", name, i+1, len(ids)), float64(i)/float64(max(1, len(ids))))
			err = s.rebuildDocument(ctx, user, doc)
		}
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return nil, err
			}
			reason := "could not rebuild; the previous index was kept"
			var ae *apiError
			if errors.As(err, &ae) {
				reason = ae.message
			}
			if notFound(err) {
				reason = "document not found"
			}
			failures = append(failures, name+": "+reason)
			s.log.Warn("document rebuild failed", "document", id, "error", err)
		} else {
			count++
		}
	}
	s.jobAudit(ctx, user, "settings.update", "index", str(input, "job_id"), M{"action": "reindex_completed", "reindexed": count, "failed": len(failures)})
	return reindexSummary(count, failures, model), nil
}

func (s *Server) rebuildDocument(ctx context.Context, user string, doc M) error {
	id, original, oldIndex := str(doc, "id"), str(doc, "memory_id"), documentSearchMemory(doc)
	if str(doc, "status") == "deleting" {
		return fail(409, "document deletion is in progress")
	}
	if job := str(doc, "ingest_job_id"); job != "" {
		var busy bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cc_jobs WHERE id=$1 AND status IN ('pending','running'))`, job).Scan(&busy); err != nil {
			return err
		}
		if busy {
			return fail(409, "initial OCR is still in progress; try again after it finishes")
		}
	}
	data, err := s.gm.Content(ctx, original)
	if err != nil {
		return err
	}
	contentType := str(doc, "content_type")
	ocr := doc["ocr_required"] == true
	if ocr {
		text, current, e := s.documentText(ctx, user, id)
		if e != nil {
			return e
		}
		if str(current, "memory_id") != original {
			return fail(409, "document changed during text extraction")
		}
		data = []byte(text)
		contentType = "text/plain"
	}
	if len(data) == 0 {
		return fail(422, "the stored document has no content")
	}
	index := uuid()
	lease, ok := ctx.Value(jobLeaseKey{}).(jobLease)
	if !ok {
		return fail(500, "A rebuild requires a durable job")
	}
	// Register cleanup before any external write. While the job runs its new
	// memory is protected; after a crash/cancellation, maintenance removes it.
	if err = s.kvSet(ctx, "memory-delete:"+index, M{"memory_id": index, "job_id": lease.ID, "not_before": time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339Nano)}, 0); err != nil {
		return err
	}
	if err = s.maintenanceAllowed(ctx, user, "documents.upload"); err != nil {
		return err
	}
	if _, err = s.gm.CreateReindex(ctx, index, id, original, str(doc, "filename"), contentType, data, ocr); err != nil {
		return err
	}
	if _, err = s.gm.Wait(ctx, index); err != nil {
		return err
	}
	// A COMPLETED memory must also return a searchable chunk. This catches an
	// empty extraction instead of replacing a working index with zero passages.
	hits, err := s.gm.SearchWithRerank(ctx, str(doc, "filename"), []string{index}, 1, false)
	if err != nil {
		return err
	}
	if len(hits) == 0 {
		return fail(422, "rebuilding produced no searchable text; the previous index was kept")
	}
	if err = s.maintenanceAllowed(ctx, user, "documents.upload"); err != nil {
		return err
	}
	if oldIndex != original {
		if err = s.scheduleMemoryDeletion(ctx, oldIndex); err != nil {
			return err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var active string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM cc_jobs WHERE id=$1 AND user_id=$2 AND lease_token=$3 AND lease_until>now() FOR UPDATE`, lease.ID, user, lease.Token).Scan(&active); err != nil {
		return err
	}
	if active != "running" {
		return context.Canceled
	}
	patch := M{"index_memory_id": index, "status": "indexed", "indexed_at": nowString(), "error": nil, "processing_status": "COMPLETED", "ingest_job_id": nil, "chunk_count": 0, "chunk_count_known": false}
	r, err := tx.ExecContext(ctx, `UPDATE cc_objects SET data=data || $5::jsonb,updated_at=now() WHERE kind='document' AND id=$1 AND owner_id=$2 AND data->>'memory_id'=$3 AND COALESCE(NULLIF(data->>'index_memory_id',''),data->>'memory_id')=$4 AND data->>'status'<>'deleting' AND EXISTS(SELECT 1 FROM cc_users WHERE id=$2 AND active)`, id, user, original, oldIndex, jsonBytes(patch))
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return fail(409, "the document was deleted or changed during rebuilding")
	}
	return tx.Commit()
}

func clearSummary(cleared, failed int) (M, error) {
	if failed > 0 && cleared == 0 {
		return nil, fail(502, fmt.Sprintf("None of your %d document records could be removed. They were kept so you can retry cleanup.", failed))
	}
	if failed > 0 {
		return M{"status": "partial", "message": fmt.Sprintf("Removed %d document(s). %d could not be completely removed and were kept; try again.", cleared, failed), "cleared_documents": cleared, "failed_documents": failed}, nil
	}
	return M{"status": "cleared", "message": "Index cleared successfully. All your documents have been removed.", "cleared_documents": cleared, "failed_documents": 0}, nil
}

func (s *Server) clearDocuments(q request) (any, error) {
	if err := permit(q, "documents.delete"); err != nil {
		return nil, err
	}
	ids, err := ownedDocumentIDs(q.r.Context(), s.db, q.user.ID)
	if err != nil {
		return nil, err
	}
	cleared, failed := 0, 0
	for _, id := range ids {
		if err = s.maintenanceAllowed(q.r.Context(), q.user.ID, "documents.delete"); err != nil {
			return nil, err
		}
		r := q.r.Clone(q.r.Context())
		r.SetPathValue("id", id)
		if _, err = s.deleteDocument(request{q.w, r, q.user}); err != nil {
			failed++
			s.log.Warn("clear document failed", "document", id, "error", err)
		} else {
			cleared++
		}
	}
	s.audit(q.r.Context(), q.user.ID, "settings.update", "index", "", M{"action": "index_cleared", "cleared_documents": cleared, "failed_documents": failed})
	return clearSummary(cleared, failed)
}
