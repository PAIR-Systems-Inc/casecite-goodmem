package app

import (
	"context"
	"path/filepath"
	"strings"
	"time"
)

// All entry points use the same original-byte validation, GoodMem ownership
// and scanned-PDF path. A deterministic import ID makes replay idempotent.
func (s *Server) beginDocumentIngest(ctx context.Context, user, matter, id, name, source, sourceID string, data []byte, metadata M) (M, bool, error) {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." || len(name) > 255 {
		return nil, false, bad("Invalid filename")
	}
	if len(data) == 0 || int64(len(data)) > s.cfg.MaxUpload {
		return nil, false, bad("File is empty or exceeds the upload limit")
	}
	ct, err := validateDocumentUpload(data, name)
	if err != nil {
		return nil, false, err
	}
	settings, err := s.userSettings(ctx, user)
	if err != nil {
		return nil, false, err
	}
	profile, err := s.resolveIndexProfile(ctx, settings)
	if err != nil {
		return nil, false, err
	}
	mid := uuid()
	doc := M{"filename": name, "content_type": ct, "size": len(data), "memory_id": mid, "status": "processing", "source": source, "source_id": nullable(sourceID), "sha256": digest(string(data)), "chunk_count": 0, "metadata": metadata, "folder_path": nil}
	doc["index_profile"] = profile
	if documentNeedsOCR(data) {
		doc["ocr_required"] = true
		doc["index_memory_id"] = uuid()
	}
	r, err := s.db.ExecContext(ctx, `INSERT INTO cc_objects(kind,id,owner_id,matter_id,data) SELECT 'document',$1,id,$3,$4 FROM cc_users WHERE id=$2 AND active ON CONFLICT(kind,id) DO NOTHING`, id, user, nullable(matter), jsonBytes(doc))
	if err != nil {
		return nil, false, err
	}
	if inserted, _ := r.RowsAffected(); inserted == 0 {
		current, e := s.getObject(ctx, user, "document", id)
		if e != nil {
			return nil, false, e
		}
		if str(current, "sha256") != str(doc, "sha256") {
			return nil, false, fail(409, "An import already exists with a different document revision")
		}
		return current, false, nil
	}
	_, createErr := s.gm.withIndexProfile(profile).Create(ctx, mid, id, name, ct, data)
	finish, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	if createErr != nil {
		doc["status"] = "upload_uncertain"
		doc["error"] = "Upload did not finish cleanly. Refresh to reconcile with GoodMem."
	}
	// Only the reserved row is updated; a delete or account purge wins over a
	// late remote response and cannot be undone by an upsert.
	r, err = s.db.ExecContext(finish, `UPDATE cc_objects SET data=data || $3::jsonb WHERE kind='document' AND id=$1 AND data->>'memory_id'=$2 AND data->>'ingest_job_id' IS NULL`, id, mid, jsonBytes(M{"status": doc["status"], "error": doc["error"]}))
	if err != nil {
		return nil, true, err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		if _, e := s.getObject(finish, user, "document", id); e != nil {
			_ = s.scheduleMemoryDeletion(finish, mid)
			return nil, true, e
		}
	}
	if createErr == nil && doc["ocr_required"] == true {
		if _, err = s.enqueueDocumentOCR(finish, user, id, false); err != nil {
			return nil, true, err
		}
	}
	s.audit(finish, user, "document.upload", "document", id, M{"matter_id": matter, "source": source, "source_id": sourceID, "size": len(data)})
	current, err := s.getObject(finish, user, "document", id)
	return current, true, err
}

func (s *Server) waitDocumentIndexed(ctx context.Context, user, id string) (M, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Minute)
	defer cancel()
	for {
		doc, err := s.getObject(ctx, user, "document", id)
		if err != nil {
			return nil, err
		}
		doc = s.refreshDocument(ctx, doc)
		switch str(doc, "status") {
		case "indexed":
			return doc, nil
		case "failed":
			return doc, fail(502, fallback(str(doc, "error"), "The document could not be indexed"))
		}
		if err = connectorWait(ctx, 750*time.Millisecond); err != nil {
			return doc, err
		}
	}
}
