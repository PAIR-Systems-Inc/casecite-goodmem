package app

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"regexp"
	"slices"
	"sort"
	"strings"
)

//go:embed review_schemas.json
var reviewSchemasJSON []byte

var reviewFieldKeyRE = regexp.MustCompile(`[^a-z0-9]+`)

func reviewFields(raw any) ([]M, error) {
	if entries, ok := raw.([]any); ok {
		for _, entry := range entries {
			if _, ok := entry.(map[string]any); !ok {
				return nil, fail(422, "Each field must be an object")
			}
		}
	}
	fields := maps(raw)
	if len(fields) < 1 || len(fields) > 40 {
		return nil, fail(422, "Provide between 1 and 40 fields")
	}
	seen := map[string]bool{}
	for i, f := range fields {
		name, ok := f["name"].(string)
		if !ok || strings.TrimSpace(name) == "" || len([]rune(name)) > 120 {
			return nil, fail(422, "Field names must contain 1 to 120 characters")
		}
		description := str(f, "description")
		if raw, exists := f["description"]; exists {
			if _, ok := raw.(string); !ok {
				return nil, fail(422, "Field descriptions must be text")
			}
		}
		if len([]rune(description)) > 600 {
			return nil, fail(422, "Field descriptions must be at most 600 characters")
		}
		key := str(f, "key")
		if f["key"] != nil {
			if _, ok := f["key"].(string); !ok || key == "" {
				return nil, fail(422, "Field keys must be nonempty text")
			}
		}
		if len([]rune(key)) > 80 {
			return nil, fail(422, "Field keys must be at most 80 characters")
		}
		key = fallback(key, name)
		key = charSlice(strings.Trim(reviewFieldKeyRE.ReplaceAllString(strings.ToLower(strings.TrimSpace(key)), "_"), "_"), 0, 80)
		key = fallback(key, "field")
		if seen[key] {
			return nil, fail(422, "Duplicate field: "+key)
		}
		seen[key] = true
		typ := fallback(str(f, "type"), "text")
		if raw, exists := f["type"]; exists {
			if _, ok := raw.(string); !ok || raw == "" {
				return nil, fail(422, "Unknown field type")
			}
		}
		if reviewTypeGuidance[typ] == "" {
			return nil, fail(422, "Unknown field type: "+typ)
		}
		fields[i] = M{"key": key, "name": strings.TrimSpace(name), "description": strings.TrimSpace(description), "type": typ}
	}
	return fields, nil
}
func reviewStrings(raw any, maxItems int) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	var values []string
	switch v := raw.(type) {
	case []string:
		values = v
	case []any:
		for _, value := range v {
			s, ok := value.(string)
			if !ok {
				return nil, fail(422, "Expected a list of identifiers")
			}
			values = append(values, s)
		}
	default:
		return nil, fail(422, "Expected a list of identifiers")
	}
	if len(values) > maxItems {
		return nil, fail(422, "Too many identifiers")
	}
	out := []string{}
	for _, v := range values {
		if len(v) > 80 || v == "" {
			return nil, fail(422, "Invalid identifier")
		}
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out, nil
}

func validateReviewMetadata(in M, creating bool) error {
	if creating || in["name"] != nil {
		name, ok := in["name"].(string)
		if !ok || strings.TrimSpace(name) == "" || len([]rune(name)) > 300 {
			return fail(422, "Name must contain 1 to 300 characters")
		}
		in["name"] = strings.TrimSpace(name)
	}
	if in["description"] != nil {
		d, ok := in["description"].(string)
		if !ok || len([]rune(d)) > 2000 {
			return fail(422, "Description must be at most 2,000 characters")
		}
		in["description"] = nullable(strings.TrimSpace(d))
	}
	if creating || in["fields"] != nil {
		fields, err := reviewFields(in["fields"])
		if err != nil {
			return err
		}
		in["fields"] = fields
	}
	if in["matter_id"] != nil {
		m, ok := in["matter_id"].(string)
		if !ok || len(m) > 36 {
			return fail(422, "Invalid matter identifier")
		}
	}
	return nil
}

func reviewRole(table M, user string) string {
	if str(table, "user_id") == user {
		return "owner"
	}
	return "member"
}
func reviewSchemaResponse(m M, user string) M {
	return M{"id": m["id"], "name": m["name"], "description": m["description"], "matter_id": m["matter_id"], "owner_id": m["user_id"], "role": reviewRole(m, user), "is_builtin": false, "fields": m["fields"], "created_at": m["created_at"], "updated_at": m["updated_at"]}
}
func reviewTableSummary(m M, user string, cells []M) M {
	approved, flagged := 0, 0
	for _, c := range cells {
		if str(c, "review_status") == "approved" {
			approved++
		}
		if str(c, "review_status") == "flagged" {
			flagged++
		}
	}
	return M{"id": m["id"], "name": m["name"], "description": m["description"], "matter_id": m["matter_id"], "owner_id": m["user_id"], "role": reviewRole(m, user), "field_count": len(maps(m["fields"])), "document_count": len(stringsOf(m["document_ids"])), "cell_count": len(cells), "approved_count": approved, "flagged_count": flagged, "last_run_at": m["last_run_at"], "created_at": m["created_at"], "updated_at": m["updated_at"]}
}

func reviewCellResponse(c M) M {
	c["effective_value"] = c["value"]
	if c["override_value"] != nil {
		c["effective_value"] = c["override_value"]
	}
	return c
}
func (s *Server) reviewCells(ctx context.Context, tableID string) ([]M, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM cc_review_cells WHERE table_id=$1 ORDER BY document_id,field_key`, tableID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cells := []M{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		c := M{}
		if err = json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		cells = append(cells, reviewCellResponse(c))
	}
	return cells, rows.Err()
}
func (s *Server) reviewDocuments(ctx context.Context, user string, table M) []M {
	docs := []M{}
	for _, id := range stringsOf(table["document_ids"]) {
		d, err := s.getObject(ctx, user, "document", id)
		filename := "Unavailable document"
		if err == nil {
			filename = str(d, "filename")
		}
		docs = append(docs, M{"id": id, "filename": filename, "accessible": err == nil})
	}
	return docs
}
func (s *Server) reviewDetail(ctx context.Context, user string, table M) (M, error) {
	cells, err := s.reviewCells(ctx, str(table, "id"))
	if err != nil {
		return nil, err
	}
	out := reviewTableSummary(table, user, cells)
	out["fields"], out["cells"], out["documents"] = table["fields"], cells, s.reviewDocuments(ctx, user, table)
	return out, nil
}
func (s *Server) checkReviewDocuments(ctx context.Context, user string, ids []string) error {
	for _, id := range ids {
		if _, err := s.getObject(ctx, user, "document", id); err != nil {
			return err
		}
	}
	return nil
}

// All table writers lock the parent row, including worker results. This keeps
// human review changes, scope changes and row deletion atomic with extraction.
func lockReviewObject(ctx context.Context, tx *sql.Tx, user, kind, id string, owned bool) (M, error) {
	condition := visibleSQL
	if owned {
		condition = `o.owner_id=$1`
	}
	return scanObject(tx.QueryRowContext(ctx, `SELECT o.id,o.owner_id,COALESCE(o.matter_id,''),o.data,o.created_at,o.updated_at FROM cc_objects o WHERE `+condition+` AND o.kind=$2 AND o.id=$3 FOR UPDATE`, user, kind, id))
}

func (s *Server) reviewCollection(q request) (any, error) {
	ctx, user := q.r.Context(), q.user.ID
	schema := strings.HasSuffix(q.r.URL.Path, "/schemas")
	kind, key := "review_table", "tables"
	if schema {
		kind, key = "review_schema", "schemas"
	}
	if q.r.Method == "GET" {
		items, err := s.objects(ctx, user, kind)
		if err != nil {
			return nil, err
		}
		out := []M{}
		counts := map[string]M{}
		if !schema {
			rows, err := s.db.QueryContext(ctx, `SELECT c.table_id,count(*),count(*) FILTER(WHERE c.data->>'review_status'='approved'),count(*) FILTER(WHERE c.data->>'review_status'='flagged') FROM cc_review_cells c JOIN cc_objects o ON o.kind='review_table' AND o.id=c.table_id WHERE `+visibleSQL+` GROUP BY c.table_id`, user)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var id string
				var cells, approved, flagged int
				if err = rows.Scan(&id, &cells, &approved, &flagged); err != nil {
					rows.Close()
					return nil, err
				}
				counts[id] = M{"cell_count": cells, "approved_count": approved, "flagged_count": flagged}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, err
			}
		}
		if schema {
			if err = json.Unmarshal(reviewSchemasJSON, &out); err != nil {
				return nil, err
			}
			sort.Slice(items, func(i, j int) bool { return str(items[i], "name") < str(items[j], "name") })
		}
		for _, item := range items {
			if schema {
				out = append(out, reviewSchemaResponse(item, user))
				continue
			}
			out = append(out, mergeMap(reviewTableSummary(item, user, nil), counts[str(item, "id")]))
		}
		return M{key: out}, nil
	}
	in, err := body(q)
	if err != nil {
		return nil, err
	}
	if err = validateReviewMetadata(in, true); err != nil {
		return nil, err
	}
	matter := str(in, "matter_id")
	if err = s.checkMatter(ctx, user, matter, false); err != nil {
		return nil, err
	}
	data := M{"name": in["name"], "description": in["description"], "fields": in["fields"]}
	if !schema {
		ids, err := reviewStrings(in["document_ids"], 1000)
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 && matter != "" {
			docs, err := s.objects(ctx, user, "document")
			if err != nil {
				return nil, err
			}
			for _, d := range docs {
				if str(d, "matter_id") == matter {
					ids = append(ids, str(d, "id"))
				}
			}
		}
		if len(ids) > 1000 {
			return nil, bad("This matter contains more than 1,000 documents. Select the documents for this table.")
		}
		if err = s.checkReviewDocuments(ctx, user, ids); err != nil {
			return nil, err
		}
		if ids == nil {
			ids = []string{}
		}
		data["document_ids"] = ids
	}
	id := uuid()
	if err = s.saveObject(ctx, user, kind, id, matter, data); err != nil {
		return nil, err
	}
	s.audit(ctx, user, kind+".created", kind, id, M{"fields": len(maps(in["fields"])), "documents": len(stringsOf(data["document_ids"]))})
	item, err := s.getObject(ctx, user, kind, id)
	if err != nil {
		return nil, err
	}
	var out M
	if schema {
		out = reviewSchemaResponse(item, user)
	} else {
		out, err = s.reviewDetail(ctx, user, item)
	}
	if err != nil {
		return nil, err
	}
	writeJSON(q.w, 201, out)
	return nil, nil
}

func (s *Server) reviewItem(q request) (any, error) {
	ctx, user := q.r.Context(), q.user.ID
	id, kind := q.r.PathValue("table_id"), "review_table"
	if q.r.PathValue("schema_id") != "" {
		id, kind = q.r.PathValue("schema_id"), "review_schema"
	}
	if q.r.Method == "GET" {
		m, err := s.getObject(ctx, user, kind, id)
		if err != nil {
			return nil, err
		}
		return s.reviewDetail(ctx, user, m)
	}
	var in M
	var err error
	if q.r.Method == "PATCH" {
		in, err = body(q)
		if err != nil {
			return nil, err
		}
		if len(in) == 0 {
			return nil, bad("Nothing to update.")
		}
		if err = validateReviewMetadata(in, false); err != nil {
			return nil, err
		}
		if kind == "review_schema" {
			if err = s.checkMatter(ctx, user, str(in, "matter_id"), false); err != nil {
				return nil, err
			}
		}
		if in["document_ids"] != nil {
			ids, e := reviewStrings(in["document_ids"], 1000)
			if e != nil {
				return nil, e
			}
			if e = s.checkReviewDocuments(ctx, user, ids); e != nil {
				return nil, e
			}
			in["document_ids"] = ids
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	m, err := lockReviewObject(ctx, tx, user, kind, id, true)
	if err != nil {
		return nil, err
	}
	if q.r.Method == "DELETE" {
		if _, err = tx.ExecContext(ctx, `DELETE FROM cc_objects WHERE kind=$1 AND id=$2`, kind, id); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		s.audit(ctx, user, kind+".deleted", kind, id, M{})
		return M{"status": "deleted", "id": id}, nil
	}
	for _, key := range []string{"name", "fields", "document_ids"} {
		if in[key] != nil {
			m[key] = in[key]
		}
	}
	if _, ok := in["description"]; ok {
		m["description"] = in["description"]
	}
	if kind == "review_schema" {
		if _, ok := in["matter_id"]; ok {
			m["matter_id"] = nullable(str(in, "matter_id"))
		}
	}
	if kind == "review_table" {
		keys := []string{}
		for _, f := range maps(m["fields"]) {
			keys = append(keys, str(f, "key"))
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM cc_review_cells WHERE table_id=$1 AND (NOT (field_key=ANY($2)) OR NOT (document_id=ANY($3)))`, id, keys, stringsOf(m["document_ids"])); err != nil {
			return nil, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE cc_objects SET data=$3,matter_id=$4,updated_at=now() WHERE kind=$1 AND id=$2`, kind, id, jsonBytes(m), m["matter_id"]); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.audit(ctx, user, kind+".updated", kind, id, M{})
	m, err = s.getObject(ctx, user, kind, id)
	if err != nil {
		return nil, err
	}
	if kind == "review_schema" {
		return reviewSchemaResponse(m, user), nil
	}
	return s.reviewDetail(ctx, user, m)
}

func (s *Server) reviewTableDocuments(q request) (any, error) {
	ctx, user, id := q.r.Context(), q.user.ID, q.r.PathValue("table_id")
	var in M
	var ids []string
	var err error
	if q.r.Method == "POST" {
		in, err = body(q)
		if err != nil {
			return nil, err
		}
		ids, err = reviewStrings(in["document_ids"], 1000)
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			return nil, fail(422, "Select at least one document")
		}
		if err = s.checkReviewDocuments(ctx, user, ids); err != nil {
			return nil, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	table, err := lockReviewObject(ctx, tx, user, "review_table", id, true)
	if err != nil {
		return nil, err
	}
	docIDs, added := stringsOf(table["document_ids"]), []string{}
	if q.r.Method == "DELETE" {
		doc := q.r.PathValue("document_id")
		if !slices.Contains(docIDs, doc) {
			return nil, missing()
		}
		docIDs = slices.DeleteFunc(docIDs, func(v string) bool { return v == doc })
		if _, err = tx.ExecContext(ctx, `DELETE FROM cc_review_cells WHERE table_id=$1 AND document_id=$2`, id, doc); err != nil {
			return nil, err
		}
	} else {
		for _, doc := range ids {
			if !slices.Contains(docIDs, doc) {
				docIDs = append(docIDs, doc)
				added = append(added, doc)
			}
		}
		if len(docIDs) > 1000 {
			return nil, bad("A table supports at most 1,000 documents")
		}
	}
	table["document_ids"] = docIDs
	if _, err = tx.ExecContext(ctx, `UPDATE cc_objects SET data=$2,updated_at=now() WHERE kind='review_table' AND id=$1`, id, jsonBytes(table)); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.audit(ctx, user, "review_table.documents", "review_table", id, M{"added": len(added), "removed": q.r.Method == "DELETE"})
	var started M
	if run, _ := in["run"].(bool); run && len(added) > 0 {
		started, err = s.startReviewRun(q, id, M{"document_ids": added})
		if err != nil {
			return nil, err
		}
	}
	table, err = s.getObject(ctx, user, "review_table", id)
	if err != nil {
		return nil, err
	}
	detail, err := s.reviewDetail(ctx, user, table)
	if err != nil {
		return nil, err
	}
	if q.r.Method == "DELETE" {
		return detail, nil
	}
	return M{"added": added, "table": detail, "run": started}, nil
}

func (s *Server) reviewTableText(q request) (any, error) {
	table, err := s.getObject(q.r.Context(), q.user.ID, "review_table", q.r.PathValue("table_id"))
	if err != nil {
		return nil, err
	}
	id := q.r.PathValue("document_id")
	if !slices.Contains(stringsOf(table["document_ids"]), id) {
		return nil, missing()
	}
	text, doc, err := s.documentText(q.r.Context(), q.user.ID, id)
	if err != nil {
		return nil, err
	}
	return M{"document_id": id, "filename": doc["filename"], "text": text, "length": byteToUTF16(text, len(text))}, nil
}

func (s *Server) reviewTableRoutes() {
	for _, p := range []string{"GET /api/v1/review-tables", "POST /api/v1/review-tables", "GET /api/v1/review-tables/schemas", "POST /api/v1/review-tables/schemas"} {
		s.api(p, false, s.reviewCollection)
	}
	for _, p := range []string{"PATCH /api/v1/review-tables/schemas/{schema_id}", "DELETE /api/v1/review-tables/schemas/{schema_id}", "GET /api/v1/review-tables/{table_id}", "PATCH /api/v1/review-tables/{table_id}", "DELETE /api/v1/review-tables/{table_id}"} {
		s.api(p, false, s.reviewItem)
	}
	s.api("POST /api/v1/review-tables/{table_id}/documents", false, s.reviewTableDocuments)
	s.api("DELETE /api/v1/review-tables/{table_id}/documents/{document_id}", false, s.reviewTableDocuments)
	s.api("GET /api/v1/review-tables/{table_id}/documents/{document_id}/text", false, s.reviewTableText)
	s.api("PATCH /api/v1/review-tables/{table_id}/cells/{document_id}/{field_key}", false, s.reviewTableCell)
	s.api("POST /api/v1/review-tables/{table_id}/cells/bulk", false, s.reviewTableCell)
	s.api("GET /api/v1/review-tables/{table_id}/estimate", false, s.reviewTableEstimate)
	s.api("POST /api/v1/review-tables/{table_id}/run", false, s.reviewTableRun)
	s.api("GET /api/v1/review-tables/{table_id}/runs", false, s.reviewTableRuns)
	s.api("GET /api/v1/review-tables/{table_id}/export", false, s.reviewTableExport)
}
