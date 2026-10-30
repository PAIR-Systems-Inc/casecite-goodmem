package app

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

//go:embed workflow_templates.json
var workflowTemplatesJSON []byte

func (s *Server) workflowCaps() M {
	return M{"max_steps": positive(s.cfg.WorkflowMaxSteps, 12), "max_documents": positive(s.cfg.WorkflowMaxDocuments, 25), "max_citations": positive(s.cfg.WorkflowMaxCitations, 10), "max_output_bytes": positive(s.cfg.WorkflowMaxOutputBytes, 524288)}
}
func positive(n, fallback int) int {
	if n > 0 {
		return n
	}
	return fallback
}

func (s *Server) workflowDocumentRefs(ctx context.Context, user string, ids []string) []M {
	out := []M{}
	for _, id := range uniqueWorkflowStrings(ids) {
		doc, err := s.getObject(ctx, user, "document", id)
		out = append(out, M{"id": id, "filename": doc["filename"], "accessible": err == nil})
	}
	return out
}

func (s *Server) workflowSummary(ctx context.Context, user string, w M, detail bool) (M, error) {
	steps := maps(w["steps"])
	kinds := []string{}
	for _, step := range steps {
		kinds = append(kinds, str(step, "kind"))
	}
	out := M{"id": w["id"], "name": w["name"], "description": w["description"], "matter_id": w["matter_id"], "owner_id": w["user_id"], "role": reviewRole(w, user), "template_key": w["template_key"], "version": number(w, "version", 1), "archived_at": w["archived_at"], "step_count": len(steps), "step_kinds": kinds, "run_count": 0, "last_run_at": nil, "last_run_status": nil, "created_at": w["created_at"], "updated_at": w["updated_at"]}
	var count int
	var last, status sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT count(*),max(created_at)::text,(array_agg(status ORDER BY created_at DESC))[1] FROM cc_workflow_runs WHERE workflow_id=$1`, str(w, "id")).Scan(&count, &last, &status)
	if err != nil {
		return nil, err
	}
	out["run_count"] = count
	out["last_run_at"] = nullable(last.String)
	out["last_run_status"] = nullable(status.String)
	if detail {
		out["steps"] = steps
		out["documents"] = s.workflowDocumentRefs(ctx, user, workflowFixedDocuments(steps))
	}
	return out, nil
}

func (s *Server) validateWorkflow(ctx context.Context, user string, in M, creating bool) ([]M, error) {
	v := workflowValidator{index: -1}
	if creating || in["name"] != nil {
		out := M{}
		v.text(in, out, "name", "", 1, 300, nil, true)
		if strings.TrimSpace(str(out, "name")) == "" {
			v.issue("name", "Name cannot be blank")
		}
	}
	for key, limit := range map[string]int{"description": 2000, "change_note": 500, "matter_id": 36} {
		if in[key] != nil {
			v.text(in, M{}, key, "", 0, limit, nil, false)
		}
	}
	if len(v.issues) > 0 {
		return nil, fail(422, str(v.issues[0], "field")+": "+str(v.issues[0], "message"))
	}
	if !creating && in["steps"] == nil {
		return nil, nil
	}
	steps, issues := validateWorkflowSteps(in["steps"], number(s.workflowCaps(), "max_steps", 12))
	if len(issues) > 0 {
		return nil, fail(422, fmt.Sprintf("Step %d, %s: %s", number(issues[0], "index", -1)+1, str(issues[0], "field"), str(issues[0], "message")))
	}
	if err := s.checkReviewDocuments(ctx, user, workflowFixedDocuments(steps)); err != nil {
		return nil, err
	}
	return steps, nil
}

func (s *Server) workflowApprovers(ctx context.Context, tx *sql.Tx, w M, spec any) ([]string, error) {
	if ids, ok := spec.([]any); ok {
		return uniqueWorkflowStrings(stringsOf(ids)), nil
	}
	if ids, ok := spec.([]string); ok {
		return uniqueWorkflowStrings(ids), nil
	}
	owner := str(w, "user_id")
	if spec != "any_member" || str(w, "matter_id") == "" {
		return []string{owner}, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT owner_id FROM cc_matters WHERE id=$1 UNION SELECT user_id FROM cc_matter_members WHERE matter_id=$1 ORDER BY 1`, str(w, "matter_id"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{owner}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return uniqueWorkflowStrings(ids), rows.Err()
}

func (s *Server) checkWorkflowApprovers(ctx context.Context, tx *sql.Tx, w M, steps []M) error {
	allowed, err := s.workflowApprovers(ctx, tx, w, "any_member")
	if err != nil {
		return err
	}
	for _, step := range steps {
		for _, id := range stringsOf(object(step["params"])["approvers"]) {
			if !slices.Contains(allowed, id) {
				return fail(422, "Approvers must be the workflow's owner or members of its matter")
			}
		}
	}
	return nil
}

func saveWorkflowVersion(ctx context.Context, tx *sql.Tx, w M, user, note string) error {
	steps := maps(w["steps"])
	names := []string{}
	for _, step := range steps {
		names = append(names, str(step, "name"))
	}
	version := number(w, "version", 1)
	data := M{"version": version, "name": w["name"], "description": w["description"], "steps": steps, "step_count": len(steps), "step_names": names, "created_by": user, "created_at": nowString(), "change_note": nullable(note)}
	_, err := tx.ExecContext(ctx, `INSERT INTO cc_workflow_versions(workflow_id,version,data) VALUES($1,$2,$3)`, str(w, "id"), version, jsonBytes(data))
	return err
}

func (s *Server) createWorkflow(q request, in M) (any, error) {
	ctx, user := q.r.Context(), q.user.ID
	steps, err := s.validateWorkflow(ctx, user, in, true)
	if err != nil {
		return nil, err
	}
	if err = s.checkMatter(ctx, user, str(in, "matter_id"), false); err != nil {
		return nil, err
	}
	w := M{"id": uuid(), "user_id": user, "matter_id": in["matter_id"], "name": strings.TrimSpace(str(in, "name")), "description": in["description"], "steps": steps, "version": 1, "archived_at": nil, "template_key": in["template_key"]}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = s.checkWorkflowApprovers(ctx, tx, w, steps); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO cc_objects(kind,id,owner_id,matter_id,data) VALUES('workflow',$1,$2,$3,$4)`, str(w, "id"), user, nullable(str(w, "matter_id")), jsonBytes(w)); err != nil {
		return nil, err
	}
	if err = saveWorkflowVersion(ctx, tx, w, user, str(in, "change_note")); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.audit(ctx, user, "workflow_created", "workflow", str(w, "id"), M{"steps": len(steps), "matter_id": w["matter_id"]})
	w, err = s.getObject(ctx, user, "workflow", str(w, "id"))
	if err != nil {
		return nil, err
	}
	out, err := s.workflowSummary(ctx, user, w, true)
	if err != nil {
		return nil, err
	}
	writeJSON(q.w, 201, out)
	return nil, nil
}

func (s *Server) workflowCollection(q request) (any, error) {
	if q.r.Method == "POST" {
		in, err := body(q)
		if err != nil {
			return nil, err
		}
		delete(in, "template_key")
		return s.createWorkflow(q, in)
	}
	items, err := s.objects(q.r.Context(), q.user.ID, "workflow")
	if err != nil {
		return nil, err
	}
	out := []M{}
	include := q.r.URL.Query().Get("include_archived") == "true"
	for _, w := range items {
		if !include && w["archived_at"] != nil {
			continue
		}
		if m := q.r.URL.Query().Get("matter_id"); m != "" && str(w, "matter_id") != m {
			continue
		}
		summary, err := s.workflowSummary(q.r.Context(), q.user.ID, w, false)
		if err != nil {
			return nil, err
		}
		out = append(out, summary)
	}
	return M{"workflows": out}, nil
}

func (s *Server) workflowItem(q request) (any, error) {
	ctx, user, id := q.r.Context(), q.user.ID, q.r.PathValue("id")
	if q.r.Method == "GET" {
		w, err := s.getObject(ctx, user, "workflow", id)
		if err != nil {
			return nil, err
		}
		return s.workflowSummary(ctx, user, w, true)
	}
	in := M{}
	var err error
	var steps []M
	if q.r.Method == "PATCH" {
		in, err = body(q)
		if err != nil {
			return nil, err
		}
		steps, err = s.validateWorkflow(ctx, user, in, false)
		if err != nil {
			return nil, err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	w, err := lockReviewObject(ctx, tx, user, "workflow", id, true)
	if err != nil {
		return nil, err
	}
	if q.r.Method == "DELETE" {
		if _, err = tx.ExecContext(ctx, `DELETE FROM cc_objects WHERE kind='workflow' AND id=$1`, id); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		s.audit(ctx, user, "workflow_deleted", "workflow", id, nil)
		return M{"status": "deleted", "id": id}, nil
	}
	if in["name"] != nil {
		w["name"] = strings.TrimSpace(str(in, "name"))
	}
	if d, exists := in["description"]; exists {
		w["description"] = d
	}
	if steps != nil {
		if err = s.checkWorkflowApprovers(ctx, tx, w, steps); err != nil {
			return nil, err
		}
		if string(jsonBytes(w["steps"])) != string(jsonBytes(steps)) {
			w["steps"] = steps
			w["version"] = number(w, "version", 1) + 1
			if err = saveWorkflowVersion(ctx, tx, w, user, str(in, "change_note")); err != nil {
				return nil, err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE cc_objects SET data=$2,updated_at=now() WHERE kind='workflow' AND id=$1`, id, jsonBytes(w)); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.audit(ctx, user, "workflow_updated", "workflow", id, M{"version": w["version"]})
	w, err = s.getObject(ctx, user, "workflow", id)
	if err != nil {
		return nil, err
	}
	return s.workflowSummary(ctx, user, w, true)
}

func (s *Server) workflowAction(q request) (any, error) {
	ctx, user, id := q.r.Context(), q.user.ID, q.r.PathValue("id")
	action := q.r.PathValue("action")
	if id == "from-template" {
		templates := []M{}
		if err := json.Unmarshal(workflowTemplatesJSON, &templates); err != nil {
			return nil, err
		}
		for _, t := range templates {
			if str(t, "key") == action {
				t["template_key"] = t["key"]
				t["matter_id"] = nullable(q.r.URL.Query().Get("matter_id"))
				return s.createWorkflow(q, t)
			}
		}
		return nil, fail(404, "Template not found")
	}
	if action == "duplicate" {
		w, err := s.getObject(ctx, user, "workflow", id)
		if err != nil {
			return nil, err
		}
		in := M{}
		if q.r.ContentLength != 0 {
			in, err = body(q)
			if err != nil {
				return nil, err
			}
		}
		in["name"] = fallback(str(in, "name"), charSlice(str(w, "name")+" (copy)", 0, 300))
		in["description"] = w["description"]
		in["steps"] = w["steps"]
		in["template_key"] = w["template_key"]
		in["change_note"] = fmt.Sprintf("Duplicated from '%s' version %d", str(w, "name"), number(w, "version", 1))
		return s.createWorkflow(q, in)
	}
	if action != "archive" && action != "unarchive" {
		return nil, fail(404, "Not found")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	w, err := lockReviewObject(ctx, tx, user, "workflow", id, true)
	if err != nil {
		return nil, err
	}
	w["archived_at"] = nil
	if action == "archive" {
		w["archived_at"] = nowString()
	}
	if _, err = tx.ExecContext(ctx, `UPDATE cc_objects SET data=$2,updated_at=now() WHERE kind='workflow' AND id=$1`, id, jsonBytes(w)); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.audit(ctx, user, "workflow_"+action, "workflow", id, nil)
	w, err = s.getObject(ctx, user, "workflow", id)
	if err != nil {
		return nil, err
	}
	return s.workflowSummary(ctx, user, w, true)
}

func (s *Server) workflowVersions(q request) (any, error) {
	ctx, user, id := q.r.Context(), q.user.ID, q.r.PathValue("id")
	w, err := s.getObject(ctx, user, "workflow", id)
	if err != nil {
		return nil, err
	}
	version := q.r.PathValue("version")
	if version != "" {
		n, e := strconv.Atoi(version)
		if e != nil || n < 1 {
			return nil, fail(422, "Invalid version")
		}
		var b []byte
		if err = s.db.QueryRowContext(ctx, `SELECT data FROM cc_workflow_versions WHERE workflow_id=$1 AND version=$2`, id, n).Scan(&b); err != nil {
			return nil, err
		}
		v := M{}
		if err = json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		if q.r.Method == "GET" {
			return v, nil
		}
		if str(w, "user_id") != user {
			return nil, fail(404, "Workflow not found")
		}
		in := M{}
		if q.r.ContentLength != 0 {
			in, err = body(q)
			if err != nil {
				return nil, err
			}
		}
		in["steps"] = v["steps"]
		if in["change_note"] == nil {
			in["change_note"] = fmt.Sprintf("Restored version %d", n)
		}
		// Reuse the same validation and locked version increment as a normal edit.
		q.r = q.r.Clone(ctx)
		q.r.Method = "PATCH"
		q.r.Body = http.NoBody
		return s.restoreWorkflow(q, in)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM cc_workflow_versions WHERE workflow_id=$1 ORDER BY version DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := []M{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		v := M{}
		if err = json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		delete(v, "steps")
		versions = append(versions, v)
	}
	return M{"current": number(w, "version", 1), "versions": versions}, rows.Err()
}

func (s *Server) restoreWorkflow(q request, in M) (any, error) {
	// The request is local JSON, never forwarded to an external endpoint.
	q.r.Body = io.NopCloser(strings.NewReader(string(jsonBytes(in))))
	q.r.ContentLength = int64(len(jsonBytes(in)))
	return s.workflowItem(q)
}

func (s *Server) workflowRoutes() {
	s.api("GET /api/v1/workflows", false, s.workflowCollection)
	s.api("POST /api/v1/workflows", false, s.workflowCollection)
	s.api("POST /api/v1/workflows/validate", false, func(q request) (any, error) {
		in, err := body(q)
		if err != nil {
			return nil, err
		}
		_, issues := validateWorkflowSteps(in["steps"], number(s.workflowCaps(), "max_steps", 12))
		return M{"valid": len(issues) == 0, "issues": issues}, nil
	})
	s.api("GET /api/v1/workflows/templates", false, func(q request) (any, error) {
		templates := []M{}
		err := json.Unmarshal(workflowTemplatesJSON, &templates)
		return M{"templates": templates}, err
	})
	for _, method := range []string{"GET", "PATCH", "DELETE"} {
		s.api(method+" /api/v1/workflows/{id}", false, s.workflowItem)
	}
	s.api("POST /api/v1/workflows/{id}/{action}", false, s.workflowAction)
	s.api("GET /api/v1/workflows/{id}/versions", false, s.workflowVersions)
	s.api("GET /api/v1/workflows/{id}/versions/{version}", false, s.workflowVersions)
	s.api("POST /api/v1/workflows/{id}/versions/{version}/restore", false, s.workflowVersions)
	s.api("GET /api/v1/workflows/runs", false, s.workflowRuns)
	s.api("GET /api/v1/workflows/{id}/runs", false, s.workflowRuns)
	s.api("GET /api/v1/workflows/{id}/runs/{run}", false, s.workflowRuns)
	s.api("POST /api/v1/workflows/{id}/run", false, s.workflowStart)
	for _, action := range []string{"resume", "approve", "reject"} {
		a := action
		s.api("POST /api/v1/workflows/{id}/runs/{run}/"+a, false, func(q request) (any, error) { return s.workflowContinue(q, a, -1) })
	}
	for _, action := range []string{"retry", "skip"} {
		a := action
		s.api("POST /api/v1/workflows/{id}/runs/{run}/steps/{index}/"+a, false, func(q request) (any, error) {
			i, err := strconv.Atoi(q.r.PathValue("index"))
			if err != nil || i < 0 {
				return nil, fail(422, "Invalid step index")
			}
			return s.workflowContinue(q, a, i)
		})
	}
}
