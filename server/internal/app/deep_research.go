package app

import (
	"context"
	"database/sql"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var researchQuestionID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,16}$`)

func researchBool(value any) (bool, bool) {
	switch v := value.(type) {
	case bool:
		return v, true
	case int:
		if v == 0 || v == 1 {
			return v == 1, true
		}
	case float64:
		if v == 0 || v == 1 {
			return v == 1, true
		}
	case string:
		switch strings.ToLower(v) {
		case "true", "t", "yes", "y", "on", "1":
			return true, true
		case "false", "f", "no", "n", "off", "0":
			return false, true
		}
	}
	return false, false
}

func researchStringList(raw any, maxItems int) ([]string, error) {
	var values []string
	switch list := raw.(type) {
	case nil:
		return nil, nil
	case []string:
		values = list
	case []any:
		for _, item := range list {
			value, ok := item.(string)
			if !ok {
				return nil, fail(422, "Expected a list of strings")
			}
			values = append(values, value)
		}
	default:
		return nil, fail(422, "Expected a list of strings")
	}
	if len(values) > maxItems {
		return nil, fail(422, "Too many values")
	}
	return values, nil
}

func validateResearchRequest(in M, running bool, maxQuestions int) (string, []M, error) {
	question := strings.TrimSpace(str(in, "question"))
	if n := len([]rune(question)); n < 3 || n > 4000 {
		return "", nil, fail(422, "question must contain 3–4000 characters")
	}
	if in["matter_id"] != nil {
		if _, ok := in["matter_id"].(string); !ok {
			return "", nil, fail(422, "matter_id must be a string")
		}
	}
	if in["document_filter"] != nil {
		f := object(in["document_filter"])
		if _, ok := in["document_filter"].(map[string]any); !ok {
			if _, ok = in["document_filter"].(M); !ok {
				return "", nil, fail(422, "document_filter must be an object")
			}
		}
		for _, key := range []string{"document_ids", "folder_paths", "sources", "doc_types", "matter_ids"} {
			if f[key] == nil {
				continue
			}
			cap := 1 << 20
			if key == "matter_ids" {
				cap = 200
			}
			values, err := researchStringList(f[key], cap)
			if err != nil {
				return "", nil, fail(422, "Invalid document_filter."+key)
			}
			if key == "sources" {
				for _, value := range values {
					if !slices.Contains(strings.Fields("google_drive onedrive box netdocuments imanage filevine clio dropbox local google_picker onedrive_picker box_picker dropbox_chooser"), value) {
						return "", nil, fail(422, "Unknown document source")
					}
				}
			}
		}
		for _, key := range []string{"search_all", "include_subfolders"} {
			if value, present := f[key]; present {
				parsed, ok := researchBool(value)
				if !ok {
					return "", nil, fail(422, "document_filter."+key+" must be a boolean")
				}
				f[key] = parsed
			}
		}
	}
	if !running {
		return question, nil, nil
	}
	raw := maps(in["plan"])
	if list, ok := in["plan"].([]any); ok && len(list) != len(raw) {
		return "", nil, fail(422, "Every sub-question must be an object")
	}
	if len(raw) < 1 || len(raw) > 20 {
		return "", nil, fail(422, "plan must contain 1–20 sub-questions")
	}
	if len(raw) > maxQuestions {
		return "", nil, bad(fmt.Sprintf("A plan may have at most %d sub-questions on this server.", maxQuestions))
	}
	seen, plan := map[string]bool{}, []M{}
	for _, sub := range raw {
		id, text := str(sub, "id"), strings.TrimSpace(str(sub, "question"))
		if !researchQuestionID.MatchString(id) || seen[id] {
			return "", nil, fail(422, "Sub-question ids must be unique and contain 1–16 letters, digits, hyphens or underscores")
		}
		if n := len([]rune(text)); n < 3 || n > 1000 {
			return "", nil, fail(422, "Sub-questions must contain 3–1000 characters")
		}
		tools, err := researchStringList(sub["tools"], 2)
		if err != nil || len(tools) == 0 {
			return "", nil, fail(422, "Each sub-question needs one or two research tools")
		}
		for _, tool := range tools {
			if tool != "documents" && tool != "case_law" {
				return "", nil, fail(422, "Unknown research tool")
			}
		}
		seen[id] = true
		plan = append(plan, M{"id": id, "question": text, "tools": uniqueWorkflowStrings(tools)})
	}
	return question, plan, nil
}

func researchFolder(doc M) string {
	folder := str(doc, "folder_path")
	metadata := object(doc["metadata"])
	if folder == "" {
		folder = fallback(str(metadata, "folder_path"), str(metadata, "parent_folder"))
	}
	if folder == "" && strings.Contains(str(metadata, "path"), "/") {
		folder = path.Dir(str(metadata, "path"))
	}
	if folder == "" {
		folder = "/" + fallback(str(doc, "source"), "local")
	}
	return strings.TrimRight(path.Clean(folder), "/") + "/"
}

// An explicit document pick and folders form a union, with source/type filters
// applied to folder matches. This mirrors the original document-filter service.
func researchFilterMatches(doc, filter M) bool {
	if filter["document_ids"] != nil {
		if slices.Contains(stringsOf(filter["document_ids"]), str(doc, "id")) {
			return true
		}
		if len(stringsOf(filter["folder_paths"])) == 0 {
			return false
		}
	}
	if filter["sources"] != nil && !slices.Contains(stringsOf(filter["sources"]), str(doc, "source")) {
		return false
	}
	if filter["doc_types"] != nil && !slices.Contains(stringsOf(filter["doc_types"]), fallback(str(object(doc["metadata"]), "doc_type"), "document")) {
		return false
	}
	if filter["folder_paths"] == nil {
		return true
	}
	folder := researchFolder(doc)
	for _, selected := range stringsOf(filter["folder_paths"]) {
		selected = strings.TrimRight(selected, "/") + "/"
		if folder == selected || filter["include_subfolders"] != false && strings.HasPrefix(folder, selected) {
			return true
		}
	}
	return false
}

func (s *Server) resolveResearchScope(ctx context.Context, user string, in M) ([]string, int, error) {
	matter := str(in, "matter_id")
	if err := s.checkMatter(ctx, user, matter, false); err != nil {
		return nil, 0, err
	}
	docs, err := s.scopedDocuments(ctx, user, M{})
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT m.id,m.is_personal FROM cc_matters m WHERE m.owner_id=$1 OR EXISTS(SELECT 1 FROM cc_matter_members mm WHERE mm.matter_id=m.id AND mm.user_id=$1)`, user)
	if err != nil {
		return nil, 0, err
	}
	matters := map[string]bool{}
	for rows.Next() {
		var id string
		var personal bool
		if err = rows.Scan(&id, &personal); err != nil {
			rows.Close()
			return nil, 0, err
		}
		matters[id] = personal
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	inMatter := func(doc M, id string) bool {
		personal, allowed := matters[id]
		return allowed && (str(doc, "matter_id") == id || personal && str(doc, "matter_id") == "" && str(doc, "user_id") == user)
	}
	f := object(in["document_filter"])
	var ids []string
	if f["search_all"] != true {
		if f["matter_ids"] != nil {
			ids = []string{}
			for _, selected := range stringsOf(f["matter_ids"]) {
				for _, doc := range docs {
					if inMatter(doc, selected) {
						ids = append(ids, str(doc, "id"))
					}
				}
			}
		}
		explicit := false
		for _, key := range []string{"document_ids", "folder_paths", "sources", "doc_types"} {
			explicit = explicit || f[key] != nil
		}
		if explicit {
			if ids == nil {
				ids = []string{}
			}
			for _, doc := range docs {
				if researchFilterMatches(doc, f) {
					ids = append(ids, str(doc, "id"))
				}
			}
		}
	}
	if ids == nil && matter != "" {
		ids = []string{}
		for _, doc := range docs {
			if inMatter(doc, matter) {
				ids = append(ids, str(doc, "id"))
			}
		}
	}
	if ids == nil {
		return nil, len(docs), nil
	}
	ids = uniqueWorkflowStrings(ids)
	return ids, len(ids), nil
}

func (s *Server) researchScope(ctx context.Context, user, matter string, ids []string, count int) (M, error) {
	key, err := s.providerKey(ctx, user, "courtlistener", s.cfg.CourtListenerToken)
	return M{"matter_id": nullable(matter), "document_ids": ids, "documents_in_scope": count, "case_law_available": true, "citation_check_available": key != ""}, err
}

func (s *Server) requireResearchModel(ctx context.Context, user string) error {
	_, err := s.utilityModel(ctx, user)
	return err
}

func (s *Server) researchPlan(q request) (any, error) {
	in, err := body(q)
	if err != nil {
		return nil, err
	}
	question, _, err := validateResearchRequest(in, false, number(s.researchCaps(), "max_subquestions", 8))
	if err != nil {
		return nil, err
	}
	ids, count, err := s.resolveResearchScope(q.r.Context(), q.user.ID, in)
	if err != nil {
		return nil, err
	}
	if err = s.requireResearchModel(q.r.Context(), q.user.ID); err != nil {
		return nil, err
	}
	plan, stats, err := s.planDeepResearchWithStats(q.r.Context(), q.user.ID, question)
	if err != nil {
		s.log.Warn("research planning failed", "error", err)
		return nil, fail(502, "The research plan could not be drafted. Please try again.")
	}
	scope, err := s.researchScope(q.r.Context(), q.user.ID, str(in, "matter_id"), ids, count)
	return M{"question": question, "plan": plan, "scope": scope, "caps": s.researchCaps(), "model": stats["model"]}, err
}

func (s *Server) startResearch(q request) (any, error) {
	in, err := body(q)
	if err != nil {
		return nil, err
	}
	question, plan, err := validateResearchRequest(in, true, number(s.researchCaps(), "max_subquestions", 8))
	if err != nil {
		return nil, err
	}
	ctx, user := q.r.Context(), q.user.ID
	ids, count, err := s.resolveResearchScope(ctx, user, in)
	if err != nil {
		return nil, err
	}
	if err = s.requireResearchModel(ctx, user); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,1))`, user); err != nil {
		return nil, err
	}
	var queued int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM cc_jobs WHERE user_id=$1 AND kind<>'document.ocr_index' AND status IN ('pending','running')`, user).Scan(&queued); err != nil {
		return nil, err
	}
	if queued >= 5 {
		return nil, fail(429, "Five jobs are already in progress. Wait for one to finish.")
	}
	id, job := uuid(), uuid()
	scope := M{"matter_id": in["matter_id"], "document_ids": ids, "documents_in_scope": count}
	data := M{"question": question, "plan": plan, "scope": scope, "status": "running", "completed_at": nil, "result": nil, "error": nil}
	input := M{"run_id": id, "job_id": job, "matter_id": in["matter_id"], "document_ids": ids}
	if _, err = tx.ExecContext(ctx, `INSERT INTO cc_objects(kind,id,owner_id,matter_id,data) VALUES('research_run',$1,$2,$3,$4)`, id, user, nullable(str(in, "matter_id")), jsonBytes(data)); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO cc_jobs(id,user_id,kind,input) VALUES($1,$2,'deep_research.run',$3)`, job, user, jsonBytes(input)); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO cc_research_jobs(run_id,job_id) VALUES($1,$2)`, id, job); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.audit(ctx, user, "deep_research_started", "deep_research_run", id, M{"matter_id": in["matter_id"], "sub_questions": len(plan), "documents_in_scope": count})
	writeJSON(q.w, 202, M{"run_id": id, "job_id": job, "poll_url": "/api/v1/jobs/" + job, "sub_questions": len(plan)})
	return nil, nil
}

func researchRunSummary(run M, user string) M {
	var cited any
	if result := object(run["result"]); len(result) > 0 {
		cited = len(maps(result["sources"]))
	}
	return M{"id": run["id"], "owner_id": run["user_id"], "matter_id": run["matter_id"], "role": reviewRole(run, user), "question": run["question"], "status": run["status"], "sub_questions": len(maps(run["plan"])), "sources_cited": cited, "created_at": run["created_at"], "completed_at": run["completed_at"]}
}

func (s *Server) listResearch(q request) (any, error) {
	matter := q.r.URL.Query().Get("matter_id")
	if err := s.checkMatter(q.r.Context(), q.user.ID, matter, false); err != nil {
		return nil, err
	}
	limit := 50
	if raw := q.r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			return nil, fail(422, "limit must be between 1 and 200")
		}
		limit = n
	}
	runs, err := s.objects(q.r.Context(), q.user.ID, "research_run")
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(runs, func(a, b M) int { return strings.Compare(str(b, "created_at"), str(a, "created_at")) })
	out := []M{}
	for _, run := range runs {
		if matter != "" && str(run, "matter_id") != matter {
			continue
		}
		out = append(out, researchRunSummary(run, q.user.ID))
		if len(out) == limit {
			break
		}
	}
	return M{"runs": out}, nil
}

func (s *Server) researchItem(q request) (any, error) {
	ctx, user := q.r.Context(), q.user.ID
	id := q.r.PathValue("id")
	run, err := s.getObject(ctx, user, "research_run", id)
	if err != nil {
		return nil, err
	}
	var job sql.NullString
	err = s.db.QueryRowContext(ctx, `SELECT job_id FROM cc_research_jobs WHERE run_id=$1`, id).Scan(&job)
	if err != nil && !notFound(err) {
		return nil, err
	}
	if q.r.Method == "DELETE" {
		if str(run, "user_id") != user {
			return nil, sql.ErrNoRows
		}
		tx, e := s.db.BeginTx(ctx, nil)
		if e != nil {
			return nil, e
		}
		defer tx.Rollback()
		// Match the worker's lock order: queue row before the saved run.
		if _, e = tx.ExecContext(ctx, `UPDATE cc_jobs SET status='cancelled',error='Research run deleted',updated_at=now() WHERE id=$1 AND status IN ('pending','running')`, job.String); e != nil {
			return nil, e
		}
		if _, e = tx.ExecContext(ctx, `DELETE FROM cc_objects WHERE kind='research_run' AND id=$1 AND owner_id=$2`, id, user); e != nil {
			return nil, e
		}
		if e = tx.Commit(); e != nil {
			return nil, e
		}
		s.audit(ctx, user, "deep_research_deleted", "deep_research_run", id, M{})
		return M{"status": "deleted", "id": id}, nil
	}
	out := researchRunSummary(run, user)
	mergeMap(out, M{"plan": run["plan"], "scope": run["scope"], "job_id": nullable(job.String), "result": run["result"], "error": run["error"]})
	return out, nil
}

func (s *Server) executeDeepResearch(ctx context.Context, user string, input M) (M, error) {
	account, err := s.loadUser(ctx, user)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(account.Permissions, "chat.use") {
		return nil, fail(403, "Research access is no longer available")
	}
	run, err := s.getObject(ctx, user, "research_run", str(input, "run_id"))
	if err != nil {
		return nil, err
	}
	if str(run, "status") != "running" {
		return nil, bad("The research run is no longer running")
	}
	var ids []string
	if scope := object(run["scope"]); scope["document_ids"] != nil {
		ids = append([]string{}, stringsOf(scope["document_ids"])...)
	}
	s.jobProgress(ctx, str(input, "job_id"), "Searching sources and checking the research plan…", 0.05)
	result, err := s.runDeepResearch(ctx, user, str(run, "question"), str(run, "matter_id"), maps(run["plan"]), ids)
	if err != nil {
		return nil, err
	}
	result["run_id"] = run["id"]
	s.jobAudit(ctx, user, "deep_research_completed", "deep_research_run", str(run, "id"), M{"sub_questions": len(maps(result["sub_questions"])), "sources_cited": len(maps(result["sources"])), "tool_calls": object(result["scope"])["tool_calls"], "quotes_verified": object(result["verification"])["quotes_verified"]})
	return result, nil
}

func (s *Server) deepResearchRoutes() {
	s.api("POST /api/v1/deep-research/plan", false, s.researchPlan)
	s.api("POST /api/v1/deep-research/run", false, s.startResearch)
	s.api("GET /api/v1/deep-research", false, s.listResearch)
	s.api("GET /api/v1/deep-research/{id}", false, s.researchItem)
	s.api("DELETE /api/v1/deep-research/{id}", false, s.researchItem)
}
