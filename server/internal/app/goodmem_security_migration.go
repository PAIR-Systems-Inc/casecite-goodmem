package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// Run only with application replicas stopped. The administrative credential is
// read exclusively by this explicit offline command, never by server startup.
// Document IDs/citations stay stable; originals and derived memories are copied
// into private spaces, byte-verified, atomically switched, then old copies purged.
func (s *Server) MigrateGoodMemSecurity(ctx context.Context, adminFile string) error {
	if s.cfg.GoodMemSecurity == nil || adminFile == "" {
		return errors.New("migration requires GOODMEM_CONFIG_FILE and a separate GOODMEM_PROVISIONING_FILE")
	}
	raw, err := os.ReadFile(adminFile)
	if err != nil {
		return err
	}
	state := M{}
	if err = json.Unmarshal(raw, &state); err != nil {
		return err
	}
	if str(state, "api_key") == "" {
		return errors.New("provisioning file has no administrative credential")
	}
	if base := str(state, "base_url"); base != "" && base != s.cfg.GoodMemSecurity.BaseURL {
		return errors.New("provisioning and runtime configurations belong to different GoodMem servers")
	}
	root := NewGoodMem(Config{GoodMemURL: s.gm.URL, GoodMemKey: str(state, "api_key")})
	if err = s.initializeGoodMemSecurity(ctx, true); err != nil {
		return err
	}
	return s.withGoodMemLock(ctx, func(ctx context.Context) error {
		var busy bool
		if err = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cc_jobs WHERE status='running' AND lease_until>now())`).Scan(&busy); err != nil {
			return err
		}
		if busy {
			return errors.New("stop all application replicas and wait for active job leases to expire before migrating")
		}
		// A checkpoint protects both copies and makes a failed cutover resumable.
		ids, err := s.goodMemDocumentIDs(ctx, `COALESCE(o.data->>'goodmem_security','')<>'1'`)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err = s.migrateGoodMemDocument(ctx, root, id); err != nil {
				return fmt.Errorf("migrate document %s: %w", id, err)
			}
		}
		if err = s.finishGoodMemMigration(ctx, root); err != nil {
			return err
		}
		return s.reconcileGoodMemSpaces(ctx, nil)
	})
}

func (s *Server) migrateGoodMemDocument(ctx context.Context, root *GoodMem, id string) error {
	doc, err := scanObject(s.db.QueryRowContext(ctx, `SELECT id,owner_id,COALESCE(matter_id,''),data,created_at,updated_at FROM cc_objects WHERE kind='document' AND id=$1`, id))
	if err != nil {
		return err
	}
	user := str(doc, "user_id")
	ctx = goodMemUserContext(ctx, user)
	checkpoint := M{}
	key := "goodmem-migration:" + id
	err = s.kvGet(ctx, key, &checkpoint)
	if notFound(err) {
		checkpoint = M{"old_original": str(doc, "memory_id"), "old_index": str(doc, "index_memory_id"), "original": uuid(), "index": uuid()}
		err = s.kvSet(ctx, key, checkpoint, 0)
	}
	if err != nil {
		return err
	}
	oldOriginal, newOriginal := str(checkpoint, "old_original"), str(checkpoint, "original")
	if oldOriginal == "" || oldOriginal != str(doc, "memory_id") {
		return errors.New("document changed since the migration checkpoint")
	}
	content, err := root.Content(ctx, oldOriginal)
	if err != nil {
		return err
	}
	if hash := str(doc, "sha256"); hash != "" && digest(string(content)) != hash {
		return errors.New("stored original does not match its recorded checksum")
	}
	meta, err := root.Memory(ctx, oldOriginal)
	if err != nil {
		return err
	}
	template, err := root.JSON(ctx, "GET", "/v1/spaces/"+str(meta, "spaceId"), nil)
	if err != nil {
		return err
	}
	originalProfile := M{}
	for k, v := range object(doc["index_profile"]) {
		originalProfile[k] = v
	}
	originalProfile["space_id"] = str(meta, "spaceId")
	originalProfile, err = s.secureDocumentProfileWithTemplate(ctx, id, originalProfile, template)
	if err != nil {
		return err
	}
	originalClient := s.gm.withIndexProfile(originalProfile)
	if err = migrateMemoryCopy(ctx, root, originalClient, oldOriginal, newOriginal, id, "", str(doc, "filename"), str(doc, "content_type"), content, false); err != nil {
		return err
	}
	profile := originalProfile
	newIndex := ""
	if oldIndex := str(checkpoint, "old_index"); oldIndex != "" && oldIndex != oldOriginal {
		indexMeta, e := root.Memory(ctx, oldIndex)
		if e != nil {
			return e
		}
		indexTemplate, e := root.JSON(ctx, "GET", "/v1/spaces/"+str(indexMeta, "spaceId"), nil)
		if e != nil {
			return e
		}
		profile = M{}
		for k, v := range object(doc["index_profile"]) {
			profile[k] = v
		}
		profile["space_id"] = str(indexMeta, "spaceId")
		profile, e = s.secureDocumentProfileWithTemplate(ctx, id, profile, indexTemplate)
		if e != nil {
			return e
		}
		indexBytes, e := root.Content(ctx, oldIndex)
		if e != nil {
			return e
		}
		newIndex = str(checkpoint, "index")
		if e = migrateMemoryCopy(ctx, root, s.gm.withIndexProfile(profile), oldIndex, newIndex, id, newOriginal, str(doc, "filename"), fallback(str(indexMeta, "contentType"), "text/plain"), indexBytes, doc["ocr_required"] == true); e != nil {
			return e
		}
		if _, e = root.Wait(ctx, newIndex); e != nil {
			return e
		}
	} else if !documentRequiresTextIndex(doc) && str(doc, "status") == "indexed" {
		if _, err = root.Wait(ctx, newOriginal); err != nil {
			return err
		}
	}
	patch := M{"memory_id": newOriginal, "index_memory_id": nullable(newIndex), "index_profile": profile, "original_space_id": str(originalProfile, "space_id"), "goodmem_security": 1, "ingest_job_id": nil}
	if documentRequiresTextIndex(doc) && newIndex == "" {
		patch["status"] = "failed"
		patch["error"] = "Security migration preserved the original. Retry text indexing to finish extraction."
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, `UPDATE cc_objects SET data=data || $3::jsonb,updated_at=now() WHERE kind='document' AND id=$1 AND data->>'memory_id'=$2`, id, oldOriginal, jsonBytes(patch))
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return errors.New("document changed during cutover")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE cc_document_text SET memory_id=$2 WHERE document_id=$1 AND memory_id=$3`, id, newOriginal, oldOriginal); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE cc_jobs SET status='cancelled',error='Indexing was interrupted by the offline GoodMem security migration. Retry explicitly.',updated_at=now() WHERE status IN ('pending','running') AND (input->>'document_id'=$1 OR kind='document.reindex' AND user_id=$2)`, id, user); err != nil {
		return err
	}
	checkpoint["committed"] = true
	if _, err = tx.ExecContext(ctx, `UPDATE cc_kv SET value=$2 WHERE key=$1`, key, jsonBytes(checkpoint)); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.log.Info("migrated GoodMem document boundary", "document", id)
	return nil
}

func migrateMemoryCopy(ctx context.Context, root, target *GoodMem, old, id, document, original, name, contentType string, data []byte, ocr bool) error {
	// A deterministic checkpoint ID makes an unknown create response recoverable.
	_, err := root.Memory(ctx, id)
	if goodMemStatus(err, 404) {
		if original == "" {
			_, err = target.Create(ctx, id, document, name, contentType, data)
		} else {
			_, err = target.CreateReindex(ctx, id, document, original, name, contentType, data, ocr)
		}
		if goodMemStatus(err, 409) {
			err = nil
		}
	}
	if err != nil {
		return err
	}
	copy, err := root.Content(ctx, id)
	if err != nil {
		return err
	}
	if digest(string(copy)) != digest(string(data)) {
		return errors.New("migrated content failed byte verification; original retained")
	}
	return nil
}

func (s *Server) finishGoodMemMigration(ctx context.Context, root *GoodMem) error {
	rows, err := s.db.QueryContext(ctx, `SELECT key,value FROM cc_kv WHERE key LIKE 'goodmem-migration:%' ORDER BY key`)
	if err != nil {
		return err
	}
	entries := []M{}
	for rows.Next() {
		var key string
		var raw []byte
		if err = rows.Scan(&key, &raw); err != nil {
			rows.Close()
			return err
		}
		m := M{}
		if err = json.Unmarshal(raw, &m); err != nil {
			rows.Close()
			return err
		}
		m["key"] = key
		entries = append(entries, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry["committed"] != true {
			return errors.New("unfinished GoodMem migration checkpoint")
		}
		for _, field := range []string{"old_original", "old_index"} {
			if id := str(entry, field); id != "" {
				if err = root.Delete(ctx, id); err != nil {
					return err
				}
			}
		}
		if _, err = s.db.ExecContext(ctx, `DELETE FROM cc_kv WHERE key=$1`, str(entry, "key")); err != nil {
			return err
		}
	}
	return nil
}
