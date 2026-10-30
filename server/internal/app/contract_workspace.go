package app

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

const docxMIME = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

func (s *Server) structured(ctx context.Context, user, instructions, prompt string) (M, error) {
	result, _, err := s.structuredWithStats(ctx, user, instructions, prompt)
	return result, err
}

func (s *Server) structuredWithStats(ctx context.Context, user, instructions, prompt string) (M, M, error) {
	if _, ok := ctx.Value(llmOptionsKey{}).(llmOptions); !ok {
		ctx = withLLMOptions(ctx, llmOptions{Utility: true})
	}
	answer, stats, err := s.llm(ctx, user, legalSystem+"\n"+instructions, prompt, true)
	if err != nil {
		return nil, nil, err
	}
	answer = strings.TrimSpace(answer)
	answer = strings.TrimPrefix(answer, "```json")
	answer = strings.TrimPrefix(answer, "```")
	answer = strings.TrimSuffix(answer, "```")
	var result M
	if err = json.Unmarshal([]byte(answer), &result); err != nil {
		return nil, nil, fail(502, "The model returned an invalid structured response. Please retry.")
	}
	if result == nil {
		return nil, nil, fail(502, "The model returned an empty structured response. Please retry.")
	}
	return result, stats, nil
}

func (s *Server) documentText(ctx context.Context, user, id string) (string, M, error) {
	ctx = goodMemUserContext(ctx, user)
	doc, err := s.getObject(ctx, user, "document", id)
	if err != nil {
		return "", nil, err
	}
	result, err := s.readDocumentText(ctx, doc)
	if err != nil {
		return "", doc, err
	}
	if err = result.fullTextError(); err != nil {
		return "", doc, err
	}
	current, err := s.getObject(ctx, user, "document", id)
	if err != nil {
		return "", nil, err
	}
	if str(current, "memory_id") != str(doc, "memory_id") {
		return "", nil, fail(409, "Document changed during text extraction. Reopen it.")
	}
	pages := []M{}
	for _, page := range result.Pages {
		pages = append(pages, M{"page_index": page.Index, "span_start": page.Start, "span_end": page.End, "method": page.Method})
	}
	current["text_extraction"] = M{"method": result.Method, "complete": result.Complete, "page_count": len(result.Pages), "failed_pages": result.FailedPages, "pages": pages}
	return result.Text, current, nil
}

func groundSpan(item M, text, quoteKey string) bool {
	start, end, ok := findQuote(text, str(item, quoteKey))
	item["verified"] = ok
	item["span_start"], item["span_end"] = nil, nil
	if !ok {
		return false
	}
	mergeMap(item, spanFields(text, start, end))
	item[quoteKey] = text[start:end]
	return true
}

func (s *Server) enqueueTask(ctx context.Context, user, kind string, input M) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,1))`, user); err != nil {
		return "", err
	}
	var queued int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM cc_jobs WHERE user_id=$1 AND kind<>'document.ocr_index' AND status IN ('pending','running')`, user).Scan(&queued); err != nil {
		return "", err
	}
	if queued >= 5 {
		return "", fail(429, "Five jobs are already in progress. Wait for one to finish.")
	}
	id := uuid()
	input["job_id"] = id
	if _, err = tx.ExecContext(ctx, `INSERT INTO cc_jobs(id,user_id,kind,input) VALUES($1,$2,$3,$4)`, id, user, kind, jsonBytes(input)); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Server) queueTask(q request, kind string, input M) (any, error) {
	ids := stringsOf(input["document_ids"])
	for _, id := range ids {
		if _, err := s.getObject(q.r.Context(), q.user.ID, "document", id); err != nil {
			return nil, err
		}
	}
	if err := s.checkMatter(q.r.Context(), q.user.ID, str(input, "matter_id"), false); err != nil {
		return nil, err
	}
	id, err := s.enqueueTask(q.r.Context(), q.user.ID, kind, input)
	if err != nil {
		return nil, err
	}
	responseType := strings.TrimPrefix(kind, "contract.") + "_started"
	switch kind {
	case "contract.analysis":
		if str(input, "mode") == "redline" {
			responseType = "redline_started"
		}
	case "contract.plan":
		responseType = "draft_plan_started"
	case "contract.generate":
		responseType = "draft_started"
	}
	response := M{"type": responseType, "job_id": id, "poll_url": "/api/v1/jobs/" + id}
	writeJSON(q.w, 202, response)
	return nil, nil
}

func fallback(value, other string) string {
	if value == "" {
		return other
	}
	return value
}

func (s *Server) contractRequest(q request) (any, error) {
	in, err := body(q)
	if err != nil {
		return nil, err
	}
	path := q.r.URL.Path
	mode := str(in, "mode")
	message := str(in, "message")
	if len([]rune(message)) > 8000 {
		return nil, bad("Instructions must not exceed 8,000 characters")
	}
	ids := stringsOf(in["reference_document_ids"])
	if len(ids) > 4 {
		return nil, bad("Attach at most four reference documents")
	}
	for key, limit := range map[string]int{"document_text": 2000000, "draft_text": 500000, "focus": 2000} {
		if len([]rune(str(in, key))) > limit {
			return nil, bad(key + " is too long")
		}
	}
	if id := str(in, "document_id"); id != "" && !slices.Contains(ids, id) {
		ids = append(ids, id)
	}
	if strings.HasSuffix(path, "/compare") {
		ids = stringsOf(in["document_ids"])
		if len(ids) == 0 && str(in, "document_id_a") != "" && str(in, "document_id_b") != "" {
			ids = []string{str(in, "document_id_a"), str(in, "document_id_b")}
		}
		if len(ids) < 2 || len(ids) > 4 {
			return nil, bad("Select two to four contracts")
		}
		unique := []string{}
		for _, id := range ids {
			if !slices.Contains(unique, id) {
				unique = append(unique, id)
			}
		}
		ids = unique
		if len(ids) < 2 {
			return nil, bad("Select at least two different documents")
		}
	}
	in["document_ids"] = ids
	for _, id := range ids {
		if _, err = s.getObject(q.r.Context(), q.user.ID, "document", id); err != nil {
			return nil, err
		}
	}
	if strings.HasSuffix(path, "/practice-profile") {
		area := strings.TrimSpace(str(in, "practice_area"))
		if len([]rune(area)) < 2 || len([]rune(area)) > 200 {
			return nil, bad("Enter a practice area up to 200 characters")
		}
		profile, _, err := s.llm(withLLMOptions(q.r.Context(), llmOptions{Utility: true}), q.user.ID, legalSystem, "An attorney practicing in "+area+" is configuring their document-review assistant. Write their editable PRACTICE PROFILE: a concise practical brief of 300–500 words. Cover document types, clause types and provisions that matter in this practice, recurring red flags and traps, deadlines and notice requirements, and what a careful attorney always checks. Use plain prose and short lists, specific to their practice, with no generic commercial boilerplate. Do not assume a jurisdiction or invent client preferences.", false)
		return M{"practice_area": area, "profile": profile}, err
	}
	if strings.HasSuffix(path, "/draft-export") {
		if str(in, "text") == "" || len([]rune(str(in, "text"))) > 500000 || len([]rune(str(in, "title"))) > 300 {
			return nil, bad("Provide draft text up to 500,000 characters and a title up to 300 characters")
		}
		return sendDOCX(q, fallback(str(in, "title"), "Draft"), str(in, "text"))
	}
	if strings.HasSuffix(path, "/analyze") {
		if _, err = parseEffectiveDate(str(in, "effective_date")); err != nil {
			return nil, err
		}
		if str(in, "document_id") == "" && strings.TrimSpace(str(in, "document_text")) == "" {
			return nil, bad("Provide document_id or document_text")
		}
		if in["instructions"] == nil {
			in["instructions"] = message
		}
		if q.r.URL.Query().Get("async") == "true" {
			return s.queueTask(q, "contract.analysis", in)
		}
		return s.analyzeContract(q.r.Context(), q.user.ID, in)
	}
	if strings.HasSuffix(path, "/compare") {
		return s.queueTask(q, "contract.compare", in)
	}
	if strings.HasSuffix(path, "/draft/plan") {
		if message == "" {
			return nil, bad("Enter drafting instructions")
		}
		pages := number(in, "target_pages", 0)
		if pages < 1 || pages > 200 {
			return nil, bad("Choose 1–200 target pages")
		}
		return s.queueTask(q, "contract.plan", in)
	}
	if strings.HasSuffix(path, "/draft/generate") {
		if message == "" {
			return nil, bad("Enter drafting instructions")
		}
		if len(jsonBytes(in["plan"])) > 200000 {
			return nil, bad("The plan is too large")
		}
		plan, err := normalizeDraftPlan(object(in["plan"]), 0)
		if err != nil {
			return nil, err
		}
		in["plan"] = plan
		return s.queueTask(q, "contract.generate", in)
	}
	if message == "" {
		return nil, bad("Enter a question or instruction")
	}
	if str(in, "document_id") == "" && str(in, "draft_text") == "" && mode != "draft" {
		return nil, bad("Select a contract first — only drafting works without one")
	}
	instructions := message
	if !slices.Contains([]string{"ask", "analyze", "redline", "draft"}, mode) {
		mode, instructions = s.routeContractMessage(q.r.Context(), q.user.ID, message)
		settings, err := s.userSettings(q.r.Context(), q.user.ID)
		if err != nil {
			return nil, err
		}
		if in["mode"] == nil && mode == "redline" && instructions == "" && strings.TrimSpace(str(settings, "contract_playbook")) == "" {
			return M{"type": "clarify", "question": "Before I redline — what's the goal? Redlines drafted for a negotiation look very different from a general risk cleanup.", "options": []string{"We're negotiating — I represent the customer/client side", "We're negotiating — I represent the vendor/provider side", "Flag and fix anything risky or off-market, neutral review", "Tighten every clause as far as possible in my favor"}}, nil
		}
	}
	in["instructions"] = fallback(instructions, message)
	if mode == "analyze" || mode == "redline" {
		in["mode"] = mode
		return s.queueTask(q, "contract.analysis", in)
	}
	if mode == "draft" {
		return s.queueTask(q, "contract.draft", in)
	}
	if mode != "ask" {
		return nil, bad("Unknown contract mode")
	}
	text := str(in, "draft_text")
	if id := str(in, "document_id"); id != "" {
		text, _, err = s.documentText(q.r.Context(), q.user.ID, id)
		if err != nil {
			return nil, err
		}
	}
	return s.answerContract(q.r.Context(), q.user.ID, text, message)
}

func sendDOCX(q request, title, text string) (any, error) {
	blocks := []reportBlock{{Kind: "heading", Text: title}}
	for _, line := range strings.Split(text, "\n") {
		blocks = append(blocks, reportBlock{Kind: "text", Text: strings.TrimRight(line, "\r")})
	}
	blocks = append(blocks, reportBlock{Kind: "notice", Text: aiReviewNotice})
	b, err := reportWordDocument(blocks)
	if err != nil {
		return nil, err
	}
	q.w.Header().Set("Content-Type", docxMIME)
	q.w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": safeFilename(title) + ".docx"}))
	_, err = q.w.Write(b)
	return nil, err
}
func safeFilename(title string) string {
	title = filepath.Base(strings.ReplaceAll(title, "\\", "/"))
	title = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"|?*`, r) {
			return '-'
		}
		return r
	}, title)
	if len(title) > 100 {
		title = string([]rune(title)[:min(60, len([]rune(title)))])
	}
	return fallback(title, "casecite")
}

func (s *Server) contractAnalyses(q request) (any, error) {
	id := q.r.PathValue("id")
	if id == "" {
		limit := 50
		if raw := q.r.URL.Query().Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 200 {
				return nil, bad("limit must be between 1 and 200")
			}
			limit = n
		}
		items, err := s.objects(q.r.Context(), q.user.ID, "analysis")
		if err != nil {
			return nil, err
		}
		slices.SortStableFunc(items, func(a, b M) int { return strings.Compare(str(b, "created_at"), str(a, "created_at")) })
		summaries := []M{}
		for _, item := range items[:min(limit, len(items))] {
			summaries = append(summaries, M{"analysis_id": item["analysis_id"], "document_id": item["document_id"], "contract_type": item["contract_type"], "created_at": item["created_at"], "issues": len(maps(item["issues"]))})
		}
		return M{"analyses": summaries}, nil
	}
	m, err := s.getObject(q.r.Context(), q.user.ID, "analysis", id)
	if err != nil {
		return nil, err
	}
	if q.r.Method == "DELETE" {
		if str(m, "user_id") != q.user.ID {
			return nil, fail(404, "Analysis not found")
		}
		_, err = s.db.ExecContext(q.r.Context(), `DELETE FROM cc_objects WHERE kind='analysis' AND id=$1 AND owner_id=$2`, id, q.user.ID)
		if err == nil {
			s.audit(q.r.Context(), q.user.ID, "contract.analysis.delete", "analysis", id, M{"document_id": m["document_id"]})
		}
		return M{"status": "deleted", "id": id}, err
	}
	if strings.HasSuffix(q.r.URL.Path, "/redline-export") {
		return s.exportContractRedlines(q, m)
	}
	if strings.HasSuffix(q.r.URL.Path, "/export") {
		format := strings.ToLower(strings.TrimSpace(fallback(q.r.URL.Query().Get("format"), "md")))
		if format != "md" && format != "docx" {
			return nil, bad("Invalid format — use md or docx")
		}
		blocks := contractReportBlocks(m)
		var content []byte
		if format == "md" {
			content = []byte(reportMarkdown(blocks))
			q.w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		} else {
			content, err = reportWordDocument(blocks)
			if err != nil {
				return nil, err
			}
			q.w.Header().Set("Content-Type", docxMIME)
		}
		q.w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "contract-analysis-" + id[:min(8, len(id))] + "." + format}))
		s.audit(q.r.Context(), q.user.ID, "contract.analysis.export", "analysis", id, M{"format": format})
		_, err = q.w.Write(content)
		return nil, err
	}
	delete(m, "source_text")
	s.audit(q.r.Context(), q.user.ID, "contract.analysis.view", "analysis", id, M{"document_id": m["document_id"]})
	return m, nil
}

func (s *Server) exportContractRedlines(q request, analysis M) (any, error) {
	if str(analysis, "user_id") != q.user.ID {
		return nil, fail(404, "Analysis not found")
	}
	if len(maps(analysis["redlines"])) == 0 {
		return nil, fail(404, "No redlines stored for this analysis")
	}
	exclude := strings.Split(q.r.URL.Query().Get("exclude"), ",")
	overrides := M{}
	if q.r.Method == "POST" {
		m, err := body(q)
		if err != nil {
			return nil, err
		}
		exclude = stringsOf(m["exclude"])
		overrides = object(m["overrides"])
	}
	if len(exclude) > 2000 || len(overrides) > 2000 {
		return nil, bad("At most 2,000 redline exclusions or overrides are allowed")
	}
	for i := range exclude {
		exclude[i] = strings.TrimSpace(exclude[i])
	}
	for ref, value := range overrides {
		proposed, ok := value.(string)
		if !ok || len([]rune(ref)) > 200 || len([]rune(proposed)) > 100000 {
			return nil, bad("A redline override exceeds its allowed size")
		}
	}
	edits := []M{}
	for _, edit := range maps(analysis["redlines"]) {
		if slices.Contains(exclude, str(edit, "ref")) {
			continue
		}
		if proposed, ok := overrides[str(edit, "ref")].(string); ok && strings.TrimSpace(proposed) != "" {
			edit["proposed_text"] = proposed
		}
		edits = append(edits, edit)
	}
	var source []byte
	var err error
	if id := str(analysis, "document_id"); id != "" {
		doc, err := s.getObject(q.r.Context(), q.user.ID, "document", id)
		if err != nil {
			return nil, err
		}
		source, err = s.gm.Content(q.r.Context(), str(doc, "memory_id"))
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(filepath.Ext(str(doc, "filename")), ".docx") {
			text, _, err := s.documentText(q.r.Context(), q.user.ID, id)
			if err != nil {
				return nil, err
			}
			source, err = wordDocument("", text)
			if err != nil {
				return nil, err
			}
		}
	} else {
		source, err = wordDocument("", str(analysis, "source_text"))
		if err != nil {
			return nil, err
		}
	}
	result, err := redlineDOCX(source, edits, fallback(q.user.Name, "CaseCite reviewer"), true)
	if err != nil {
		return nil, err
	}
	q.w.Header().Set("Content-Type", docxMIME)
	q.w.Header().Set("Content-Disposition", `attachment; filename="contract-redlines.docx"`)
	q.w.Header().Set("X-CaseCite-Applied-Edits", fmt.Sprint(len(result.Applied)))
	q.w.Header().Set("X-CaseCite-Unapplied-Edits", fmt.Sprint(len(result.Skipped)))
	q.w.Header().Set("X-Redlines-Rendered", fmt.Sprint(len(result.Applied)))
	q.w.Header().Set("X-Redlines-Omitted", fmt.Sprint(len(result.Skipped)))
	s.audit(q.r.Context(), q.user.ID, "contract.redline.export", "analysis", str(analysis, "id"), M{"applied": len(result.Applied), "unapplied": len(result.Skipped)})
	_, err = q.w.Write(result.Content)
	return nil, err
}

func (s *Server) contractRoutes() {
	for _, path := range []string{"analyze", "chat", "draft/plan", "draft/generate", "practice-profile", "compare", "draft-export"} {
		s.api("POST /api/v1/contract-analysis/"+path, false, s.contractRequest)
	}
	s.api("GET /api/v1/contract-analysis/analyses", false, s.contractAnalyses)
	s.api("GET /api/v1/contract-analysis/analyses/{id}", false, s.contractAnalyses)
	s.api("DELETE /api/v1/contract-analysis/analyses/{id}", false, s.contractAnalyses)
	s.api("GET /api/v1/contract-analysis/analyses/{id}/export", false, s.contractAnalyses)
	s.api("GET /api/v1/contract-analysis/analyses/{id}/redline-export", false, s.contractAnalyses)
	s.api("POST /api/v1/contract-analysis/analyses/{id}/redline-export", false, s.contractAnalyses)
}
