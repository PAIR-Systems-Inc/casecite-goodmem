package app

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (s *Server) upload(q request) (any, error) {
	select {
	case s.uploads <- struct{}{}:
		defer func() { <-s.uploads }()
	default:
		return nil, fail(429, "Two uploads are in progress. Please wait and retry.")
	}
	if err := q.r.ParseMultipartForm(1 << 20); err != nil {
		return nil, bad("Invalid upload or file exceeds the upload limit")
	}
	defer q.r.MultipartForm.RemoveAll()
	file, header, err := q.r.FormFile("file")
	if err != nil {
		return nil, bad("A file is required")
	}
	defer file.Close()
	name := filepath.Base(strings.ReplaceAll(header.Filename, "\\", "/"))
	if name == "." || len(name) > 255 {
		return nil, bad("Invalid filename")
	}
	data, err := io.ReadAll(io.LimitReader(file, s.cfg.MaxUpload+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || int64(len(data)) > s.cfg.MaxUpload {
		return nil, bad("File is empty or exceeds the upload limit")
	}
	ct, err := validateDocumentUpload(data, name)
	if err != nil {
		return nil, err
	}
	matter := q.r.FormValue("matter_id")
	if err = s.checkMatter(q.r.Context(), q.user.ID, matter, false); err != nil {
		return nil, err
	}
	id, mid := uuid(), uuid()
	doc := M{"filename": name, "content_type": ct, "size": len(data), "memory_id": mid, "status": "processing", "source": "local", "sha256": digest(string(data)), "chunk_count": 0, "metadata": M{}, "folder_path": nil}
	if err = s.saveObject(q.r.Context(), q.user.ID, "document", id, matter, doc); err != nil {
		return nil, err
	}
	_, err = s.gm.Create(q.r.Context(), mid, id, name, ct, data)
	// A disconnected upload can have succeeded remotely. Preserve the allocated
	// memory ID and reconcile it instead of re-uploading or losing its identity.
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	if err != nil {
		doc["status"] = "upload_uncertain"
		doc["error"] = "Upload did not finish cleanly. Refresh to reconcile with GoodMem."
	} else {
		doc["status"] = "processing"
	}
	if saveErr := s.saveObject(ctx, q.user.ID, "document", id, matter, doc); saveErr != nil {
		return nil, saveErr
	}
	s.audit(ctx, q.user.ID, "document.upload", "document", id, M{"matter_id": matter, "size": len(data)})
	return s.getObject(ctx, q.user.ID, "document", id)
}

func (s *Server) refreshDocument(ctx context.Context, doc M) M {
	state := str(doc, "status")
	if state != "processing" && state != "uploading" && state != "upload_uncertain" {
		return doc
	}
	m, err := s.gm.Memory(ctx, str(doc, "memory_id"))
	if err != nil {
		return doc
	}
	switch str(m, "processingStatus") {
	case "COMPLETED":
		doc["status"] = "indexed"
		doc["indexed_at"] = nowString()
		delete(doc, "error")
	case "FAILED":
		doc["status"] = "failed"
		doc["error"] = "GoodMem could not process this document. Delete it and upload a corrected copy."
	default:
		doc["status"] = "processing"
	}
	doc["processing_status"] = m["processingStatus"]
	// Guard against a concurrent move/delete: refresh only the status fields.
	_, err = s.db.ExecContext(ctx, `UPDATE cc_objects SET data=data || $1::jsonb WHERE kind='document' AND id=$2 AND data->>'memory_id'=$3`, jsonBytes(M{"status": doc["status"], "processing_status": doc["processing_status"], "indexed_at": doc["indexed_at"], "error": doc["error"]}), str(doc, "id"), str(doc, "memory_id"))
	if err != nil {
		s.log.Warn("document status refresh failed", "error", err)
	}
	return doc
}
func (s *Server) listDocuments(q request) (any, error) {
	docs, err := s.objects(q.r.Context(), q.user.ID, "document")
	if err != nil {
		return nil, err
	}
	out := []M{}
	for _, d := range docs {
		if m := q.r.URL.Query().Get("matter_id"); m != "" && str(d, "matter_id") != m {
			continue
		}
		d = s.refreshDocument(q.r.Context(), d)
		if source := q.r.URL.Query().Get("source"); source != "" && str(d, "source") != source {
			continue
		}
		if status := q.r.URL.Query().Get("status"); status != "" && str(d, "status") != status {
			continue
		}
		out = append(out, d)
	}
	total := len(out)
	limit, offset := 100, 0
	if raw := q.r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 500 {
			return nil, bad("limit must be 1–500")
		}
		limit = n
	}
	if raw := q.r.URL.Query().Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return nil, bad("offset must not be negative")
		}
		offset = n
	}
	out = out[min(offset, total):min(offset+limit, total)]
	return M{"documents": out, "total": total, "count": len(out), "limit": limit, "offset": offset}, nil
}
func (s *Server) document(q request) (M, error) {
	return s.getObject(q.r.Context(), q.user.ID, "document", q.r.PathValue("id"))
}
func own(q request, m M) error {
	if str(m, "user_id") != q.user.ID {
		return fail(403, "Only the owner may change this item")
	}
	return nil
}
func (s *Server) deleteDocument(q request) (any, error) {
	d, err := s.document(q)
	if err != nil {
		return nil, err
	}
	if err = s.canManageDocument(q.r.Context(), q.user.ID, d); err != nil {
		return nil, err
	}
	// Delete remote content first. A retry after a SQL failure is safe (404 is success).
	if err = s.gm.Delete(q.r.Context(), str(d, "memory_id")); err != nil {
		return nil, err
	}
	_, err = s.db.ExecContext(q.r.Context(), `DELETE FROM cc_objects WHERE kind='document' AND id=$1`, str(d, "id"))
	if err == nil {
		s.audit(q.r.Context(), q.user.ID, "document.delete", "document", str(d, "id"), M{})
	}
	return M{"status": "deleted", "id": d["id"]}, err
}
func (s *Server) moveDocument(q request) (any, error) {
	d, err := s.document(q)
	if err != nil {
		return nil, err
	}
	if err = own(q, d); err != nil {
		return nil, err
	}
	m, err := body(q)
	if err != nil {
		return nil, err
	}
	matter := str(m, "matter_id")
	if err = s.checkMatter(q.r.Context(), q.user.ID, matter, false); err != nil {
		return nil, err
	}
	if err = s.saveObject(q.r.Context(), q.user.ID, "document", str(d, "id"), matter, d); err != nil {
		return nil, err
	}
	return s.document(q)
}
func (s *Server) documentContent(q request) (any, error) {
	d, err := s.document(q)
	if err != nil {
		return nil, err
	}
	b, err := s.gm.Content(q.r.Context(), str(d, "memory_id"))
	if err != nil {
		return nil, err
	}
	t, err := extractText(b, str(d, "filename"))
	if err != nil {
		return nil, fail(422, err.Error())
	}
	return M{"document_id": d["id"], "filename": d["filename"], "text": t}, nil
}
func (s *Server) documentFile(q request) (any, error) {
	d, err := s.document(q)
	if err != nil {
		return nil, err
	}
	resp, err := s.gm.do(q.r.Context(), "GET", "/v1/memories/"+url.PathEscape(str(d, "memory_id"))+"/content", nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	q.w.Header().Set("Content-Type", str(d, "content_type"))
	q.w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": str(d, "filename")}))
	q.w.Header().Set("Cache-Control", "private,no-store")
	if _, err = io.Copy(q.w, resp.Body); err != nil {
		s.log.Debug("document download interrupted")
	}
	return nil, nil
}
func attachment(w http.ResponseWriter, name, ct string, b []byte) {
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Cache-Control", "private,no-store")
	w.WriteHeader(200)
	_, _ = w.Write(b)
}
func (s *Server) pages(q request) (any, error) {
	d, err := s.document(q)
	if err != nil {
		return nil, err
	}
	p, err := s.gm.Pages(q.r.Context(), str(d, "memory_id"))
	return M{"pages": p}, err
}
func (s *Server) page(q request) (any, error) {
	d, err := s.document(q)
	if err != nil {
		return nil, err
	}
	i, err := strconv.Atoi(q.r.PathValue("index"))
	if err != nil || i < 0 {
		return nil, bad("Invalid page index")
	}
	resp, err := s.gm.Page(q.r.Context(), str(d, "memory_id"), fmt.Sprint(i), q.r.URL.Query())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	if ct != "image/png" && ct != "image/jpeg" && ct != "image/webp" {
		return nil, fail(502, "GoodMem returned an unexpected image format")
	}
	q.w.Header().Set("Content-Type", ct)
	q.w.Header().Set("Cache-Control", "private,no-store")
	_, err = io.Copy(q.w, io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		s.log.Debug("page stream interrupted")
	}
	return nil, nil
}

// Scope is always intersected with SQL permissions, including explicit IDs.
// An empty explicit scope never falls back to every document.
func (s *Server) scopedDocuments(ctx context.Context, user string, input M) ([]M, error) {
	all, err := s.objects(ctx, user, "document")
	if err != nil {
		return nil, err
	}
	requested := stringsOf(input["document_ids"])
	ids := map[string]bool{}
	for _, id := range requested {
		ids[id] = true
	}
	out := []M{}
	for _, d := range all {
		if _, explicit := input["document_ids"]; explicit && !ids[str(d, "id")] {
			continue
		}
		if m := str(input, "matter_id"); m != "" && str(d, "matter_id") != m {
			continue
		}
		if str(d, "status") == "indexed" {
			out = append(out, d)
			delete(ids, str(d, "id"))
		}
	}
	if len(ids) > 0 {
		return nil, bad("One or more selected documents are unavailable, still processing, or outside your matter access")
	}
	return out, nil
}

type SourceDocument struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Text     string `json:"text"`
	Coverage string `json:"coverage"`
}

func (s *Server) fullDocuments(ctx context.Context, user string, input M, query string) ([]SourceDocument, error) {
	docs, err := s.scopedDocuments(ctx, user, input)
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		return nil, bad("Select at least one indexed document")
	}
	if len(docs) > 12 {
		return nil, bad("Review at most 12 documents per job")
	}
	out := []SourceDocument{}
	total := 0
	for _, d := range docs {
		b, err := s.gm.Content(ctx, str(d, "memory_id"))
		if err != nil {
			return nil, err
		}
		t, readErr := extractText(b, str(d, "filename"))
		coverage := "full text"
		if readErr != nil || strings.TrimSpace(t) == "" {
			hits, err := s.gm.Search(ctx, query, []string{str(d, "memory_id")}, 20)
			if err != nil {
				return nil, err
			}
			parts := []string{}
			for _, h := range hits {
				parts = append(parts, h.Text)
			}
			t = strings.Join(parts, "\n\n")
			coverage = "retrieved excerpts only"
		}
		if len(t) == 0 {
			return nil, bad("No readable text is available for " + str(d, "filename"))
		}
		if len([]rune(t)) > 120000 {
			return nil, bad("Document is too long for a full review (120,000 characters). Use targeted research or split the document.")
		}
		total += len([]rune(t))
		if total > 180000 {
			return nil, bad("Selected documents exceed the review context budget. Review fewer documents per job.")
		}
		out = append(out, SourceDocument{str(d, "id"), str(d, "filename"), t, coverage})
	}
	return out, nil
}
