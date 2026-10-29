package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"
)

func newReviewCell(doc, key string) M {
	return M{"document_id": doc, "field_key": key, "value": nil, "quote": nil, "span_start": nil, "span_end": nil, "verified": false, "status": "not_found", "error": nil, "model_used": nil, "updated_at": nil, "override_value": nil, "review_status": "pending", "note": nil, "reviewed_by": nil, "reviewed_at": nil, "history": []M{}}
}
func reviewInScope(table M, doc, key string) bool {
	if !slices.Contains(stringsOf(table["document_ids"]), doc) {
		return false
	}
	for _, f := range maps(table["fields"]) {
		if str(f, "key") == key {
			return true
		}
	}
	return false
}
func loadReviewCell(ctx context.Context, tx *sql.Tx, table, doc, key string) (M, bool, error) {
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT data FROM cc_review_cells WHERE table_id=$1 AND document_id=$2 AND field_key=$3`, table, doc, key).Scan(&raw)
	if notFound(err) {
		return newReviewCell(doc, key), false, nil
	}
	if err != nil {
		return nil, false, err
	}
	c := M{}
	err = json.Unmarshal(raw, &c)
	return c, true, err
}
func saveReviewCell(ctx context.Context, tx *sql.Tx, table string, c M) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO cc_review_cells(table_id,document_id,field_key,data) VALUES($1,$2,$3,$4) ON CONFLICT(table_id,document_id,field_key) DO UPDATE SET data=EXCLUDED.data`, table, c["document_id"], c["field_key"], jsonBytes(c))
	return err
}
func reviewHistory(c M, kind, user string, value any, status string) {
	e := M{"at": nowString(), "by": user, "kind": kind}
	if value != nil {
		e["value"] = charSlice(value.(string), 0, 1000)
	}
	if status != "" {
		e["status"] = status
	}
	h := append(maps(c["history"]), e)
	c["history"] = h[max(0, len(h)-20):]
}
func applyReviewChange(c, in M, user string) {
	if value, given := in["override_value"]; given {
		clean := nullable(strings.TrimSpace(str(in, "override_value")))
		if value == nil {
			clean = nil
		}
		if clean != c["override_value"] {
			c["override_value"] = clean
			kind := "override"
			if clean == nil {
				kind = "clear"
			}
			reviewHistory(c, kind, user, clean, "")
		}
	}
	if status := str(in, "review_status"); status != "" && status != str(c, "review_status") {
		c["review_status"] = status
		kind := "clear"
		if status == "approved" {
			kind = "approve"
		}
		if status == "flagged" {
			kind = "flag"
		}
		reviewHistory(c, kind, user, nil, status)
	}
	if _, given := in["note"]; given {
		c["note"] = nullable(strings.TrimSpace(str(in, "note")))
	}
	c["reviewed_by"], c["reviewed_at"], c["updated_at"] = user, nowString(), nowString()
}
func applyReviewModel(c, result M, model string, textLength int, user string) bool {
	changed := c["value"] != result["value"] || c["status"] != result["status"]
	for _, key := range []string{"value", "quote", "span_start", "span_end", "verified", "status", "error"} {
		c[key] = result[key]
	}
	c["model_used"], c["updated_at"] = model, nowString()
	if str(result, "status") == "error" {
		c["model_used"] = nil
	}
	if changed {
		if c["override_value"] == nil {
			c["review_status"] = "pending"
		}
		reviewHistory(c, "model", user, result["value"], str(result, "status"))
	}
	return changed
}
func validateReviewChange(in M) error {
	given := false
	for _, spec := range []struct {
		key   string
		limit int
	}{{"override_value", 1000}, {"note", 2000}} {
		v, exists := in[spec.key]
		given = given || exists
		if v != nil {
			text, ok := v.(string)
			if !ok || len([]rune(text)) > spec.limit {
				return fail(422, "Invalid "+spec.key)
			}
		}
	}
	status, exists := in["review_status"]
	given = given || exists
	if status != nil && !slices.Contains([]string{"pending", "approved", "flagged"}, str(in, "review_status")) {
		return fail(422, "Unknown review status")
	}
	if !given {
		return fail(422, "Nothing to change.")
	}
	return nil
}
func (s *Server) reviewTableCell(q request) (any, error) {
	in, err := body(q)
	if err != nil {
		return nil, err
	}
	if err = validateReviewChange(in); err != nil {
		return nil, err
	}
	bulk := q.r.Method == "POST"
	refs := []M{{"document_id": q.r.PathValue("document_id"), "field_key": q.r.PathValue("field_key")}}
	if bulk {
		refs = maps(in["cells"])
		if len(refs) < 1 || len(refs) > 5000 {
			return nil, fail(422, "Select between 1 and 5,000 cells")
		}
	}
	for _, ref := range refs {
		if str(ref, "document_id") == "" || len(str(ref, "document_id")) > 64 || str(ref, "field_key") == "" || len(str(ref, "field_key")) > 80 {
			return nil, fail(422, "Invalid cell reference")
		}
	}
	ctx, user, id := q.r.Context(), q.user.ID, q.r.PathValue("table_id")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	table, err := lockReviewObject(ctx, tx, user, "review_table", id, false)
	if err != nil {
		return nil, err
	}
	cells := []M{}
	seen := map[[2]string]bool{}
	for _, ref := range refs {
		doc, key := str(ref, "document_id"), str(ref, "field_key")
		if seen[[2]string{doc, key}] {
			continue
		}
		seen[[2]string{doc, key}] = true
		if !reviewInScope(table, doc, key) {
			if bulk {
				continue
			}
			return nil, missing()
		}
		c, _, err := loadReviewCell(ctx, tx, id, doc, key)
		if err != nil {
			return nil, err
		}
		applyReviewChange(c, in, user)
		if err = saveReviewCell(ctx, tx, id, c); err != nil {
			return nil, err
		}
		cells = append(cells, reviewCellResponse(c))
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.audit(ctx, user, "review_table.cells.reviewed", "review_table", id, M{"cells": len(cells)})
	if bulk {
		return M{"updated": len(cells), "cells": cells}, nil
	}
	return cells[0], nil
}
