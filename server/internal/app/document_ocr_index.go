package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// GoodMem's original-file ingestion and its OCR API are separate in the pinned
// release. Scanned/mixed PDFs therefore keep the original memory for download
// and page images and use a second GoodMem memory for the complete OCR text.
// Older Excel files use this same durable path with native decoded text.
// GoodMem still owns all chunking, embeddings, storage and search.
func documentSearchMemory(doc M) string {
	return fallback(str(doc, "index_memory_id"), str(doc, "memory_id"))
}

func documentRequiresTextIndex(doc M) bool {
	return doc["ocr_required"] == true || doc["text_index_required"] == true
}

func documentNeedsOCR(content []byte) bool {
	if ocrInputFormat(content) != "PDF" {
		return false
	}
	pages, err := nativePDFPages(content)
	return err != nil || !assembleText(pages).Complete
}

func (s *Server) enqueueDocumentOCR(ctx context.Context, user, id string, retry bool) (M, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var active bool
	if err = tx.QueryRowContext(ctx, `SELECT active FROM cc_users WHERE id=$1 FOR SHARE`, user).Scan(&active); err != nil {
		return nil, err
	}
	if !active {
		return nil, fail(403, "Document owner is inactive")
	}
	var raw []byte
	var owner string
	if err = tx.QueryRowContext(ctx, `SELECT owner_id,data FROM cc_objects WHERE kind='document' AND id=$1 FOR UPDATE`, id).Scan(&owner, &raw); err != nil {
		return nil, err
	}
	if owner != user {
		return nil, fail(403, "Only the owner may retry document indexing")
	}
	doc := M{}
	if err = json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if !documentRequiresTextIndex(doc) {
		return nil, bad("This document does not require a separate text index")
	}
	if str(doc, "status") == "deleting" {
		return nil, fail(409, "Document deletion is in progress")
	}
	previous := str(doc, "ingest_job_id")
	if previous != "" {
		if !retry || str(doc, "status") != "failed" {
			return M{"job_id": previous, "status": doc["status"]}, nil
		}
	}
	index := str(doc, "index_memory_id")
	if index == "" {
		index = uuid()
	}
	job := uuid()
	input := M{"document_ids": []string{id}, "document_id": id, "memory_id": doc["memory_id"], "index_memory_id": index}
	if _, err = tx.ExecContext(ctx, `INSERT INTO cc_jobs(id,user_id,kind,input) VALUES($1,$2,'document.ocr_index',$3)`, job, user, jsonBytes(input)); err != nil {
		return nil, err
	}
	patch := M{"index_memory_id": index, "ingest_job_id": job, "status": "processing", "error": nil}
	if _, err = tx.ExecContext(ctx, `UPDATE cc_objects SET data=data || $2::jsonb,updated_at=now() WHERE kind='document' AND id=$1`, id, jsonBytes(patch)); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return M{"job_id": job, "status": "pending"}, nil
}

func (s *Server) retryDocumentOCR(q request) (any, error) {
	doc, err := s.document(q)
	if err != nil {
		return nil, err
	}
	if err = own(q, doc); err != nil {
		return nil, err
	}
	return s.enqueueDocumentOCR(q.r.Context(), q.user.ID, str(doc, "id"), true)
}

func (s *Server) executeDocumentOCR(ctx context.Context, user string, input M) (M, error) {
	id, original, index := str(input, "document_id"), str(input, "memory_id"), str(input, "index_memory_id")
	doc, err := s.getObject(ctx, user, "document", id)
	if err != nil {
		return nil, err
	}
	job, ok := ctx.Value(jobLeaseKey{}).(jobLease)
	if !ok || str(doc, "memory_id") != original || str(doc, "index_memory_id") != index || str(doc, "ingest_job_id") != job.ID {
		return nil, fail(409, "Document indexing reservation changed")
	}
	// A retry reconciles the preallocated ID before repeating an upload. A
	// complete derived memory is immutable and can be reused without new OCR.
	meta, err := s.gm.Memory(ctx, index)
	var remote *GoodMemError
	if err != nil && !errors.As(err, &remote) {
		return nil, err
	}
	if err != nil && remote.Status != 404 {
		return nil, err
	}
	if err == nil && str(meta, "processingStatus") == "FAILED" {
		if err = s.gm.Delete(ctx, index); err != nil {
			return nil, err
		}
		meta = nil
	}
	if meta == nil {
		text, current, e := s.documentText(ctx, user, id)
		if e != nil {
			return nil, e
		}
		if str(current, "memory_id") != original {
			return nil, fail(409, "Document changed during OCR")
		}
		// Check the durable lease immediately before the external write. Job
		// cancellation/deletion cannot publish the resulting index to readers.
		var valid bool
		if e = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cc_jobs j JOIN cc_objects d ON d.kind='document' AND d.id=$3 WHERE j.id=$1 AND j.lease_token=$2 AND j.status='running' AND d.data->>'ingest_job_id'=j.id AND d.data->>'memory_id'=$4)`, job.ID, job.Token, id, original).Scan(&valid); e != nil {
			return nil, e
		}
		if !valid {
			return nil, context.Canceled
		}
		s.jobProgress(ctx, job.ID, "Indexing document text in GoodMem…", 0.8)
		method := "native"
		if doc["ocr_required"] == true {
			method = "goodmem_ocr"
		}
		if _, e = s.gm.withIndexProfile(object(doc["index_profile"])).CreateTextIndex(ctx, index, id, original, str(doc, "filename"), []byte(text), method); e != nil {
			return nil, e
		}
	}
	if _, err = s.gm.Wait(ctx, index); err != nil {
		return nil, err
	}
	return M{"document_id": id, "memory_id": original, "index_memory_id": index}, nil
}

func (s *Server) scheduleMemoryDeletion(ctx context.Context, memory string) error {
	if memory == "" {
		return nil
	}
	return s.kvSet(ctx, "memory-delete:"+memory, M{"memory_id": memory, "not_before": time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339Nano)}, 0)
}

// Delay cleanup beyond the HTTP request timeout so a late remote create cannot
// race immediate deletion. PostgreSQL records survive account and job deletion.
func (s *Server) cleanupDocumentOCR(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, `SELECT key,value->>'memory_id' FROM cc_kv WHERE starts_with(key,'memory-delete:') AND (value->>'not_before')::timestamptz<now() AND NOT EXISTS(SELECT 1 FROM cc_jobs j WHERE j.id=cc_kv.value->>'job_id' AND j.status IN ('pending','running')) AND NOT EXISTS(SELECT 1 FROM cc_objects d WHERE d.kind='document' AND (d.data->>'memory_id'=cc_kv.value->>'memory_id' OR d.data->>'index_memory_id'=cc_kv.value->>'memory_id')) ORDER BY key LIMIT 20`)
	if err != nil {
		s.log.Warn("OCR index cleanup lookup failed", "error", err)
		return
	}
	items := [][2]string{}
	for rows.Next() {
		var item [2]string
		if rows.Scan(&item[0], &item[1]) == nil {
			items = append(items, item)
		}
	}
	rows.Close()
	for _, item := range items {
		if ctx.Err() != nil {
			return
		}
		if err = s.gm.Delete(ctx, item[1]); err != nil {
			continue
		}
		_, _ = s.db.ExecContext(ctx, `DELETE FROM cc_kv WHERE key=$1`, item[0])
	}
}
