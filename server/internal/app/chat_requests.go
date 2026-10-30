package app

import (
	"context"
	"encoding/json"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

type chatSettingsKey struct{}

var chatSessionID = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
var chatIntegerString = regexp.MustCompile(`^[+-]?[0-9]+(?:\.0+)?$`)

func chatText(value any, field string, limit int) (string, error) {
	text, ok := value.(string)
	if !ok || len([]rune(text)) < 1 || len([]rune(text)) > limit {
		return "", fail(422, field+" must contain 1–"+strconv.Itoa(limit)+" characters")
	}
	text = strings.Map(func(r rune) rune {
		if r < 32 && r != '\t' && r != '\n' && r != '\r' || r == 127 {
			return -1
		}
		return r
	}, strings.TrimSpace(text))
	// Check after sanitization too: the Python validator admitted a query
	// consisting solely of NUL/control characters and then sent an empty prompt.
	if strings.TrimSpace(text) == "" {
		return "", fail(422, field+" cannot be empty")
	}
	return text, nil
}

func chatInt(value any, low, high int) (int, error) {
	if value == nil {
		return 0, fail(422, "Expected an integer")
	}
	var n float64
	switch v := value.(type) {
	case float64:
		n = v
	case int:
		n = float64(v)
	case bool:
		if v {
			n = 1
		}
	case string:
		v = strings.TrimSpace(v)
		if !chatIntegerString.MatchString(v) {
			return 0, fail(422, "Expected an integer")
		}
		var err error
		n, err = strconv.ParseFloat(v, 64)
		if err != nil {
			return 0, fail(422, "Expected an integer")
		}
	default:
		return 0, fail(422, "Expected an integer")
	}
	if math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) || n < float64(low) || n > float64(high) {
		return 0, fail(422, "Integer must be between "+strconv.Itoa(low)+" and "+strconv.Itoa(high))
	}
	return int(n), nil
}

func validateChatRequest(input M, strategy bool) (M, error) {
	field, limit := "query", 100000
	if strategy {
		field, limit = "question", 10000
	}
	question, err := chatText(input[field], field, limit)
	if err != nil {
		return nil, err
	}
	in := M{field: question}
	if strategy {
		in["folder_path"], in["document_ids"] = nil, nil
		if value := input["folder_path"]; value != nil {
			folder, ok := value.(string)
			if !ok || len([]rune(folder)) > 1000 {
				return nil, fail(422, "folder_path must contain at most 1000 characters")
			}
			in["folder_path"] = folder
		}
		if value := input["document_ids"]; value != nil {
			ids, err := researchStringList(value, 1<<20)
			if err != nil {
				return nil, fail(422, "document_ids must be a list of strings")
			}
			if ids == nil {
				ids = []string{}
			}
			in["document_ids"] = ids
		}
		in["include_case_law"] = false
	} else {
		mode := "research"
		if value, present := input["mode"]; present {
			var ok bool
			mode, ok = value.(string)
			if !ok {
				return nil, fail(422, "Invalid analysis mode")
			}
		}
		if !slices.Contains([]string{"research", "case", "document", "compliance", "strategy"}, mode) {
			return nil, fail(422, "Invalid analysis mode")
		}
		in["mode"] = mode
		if value := input["judge_id"]; value != nil {
			id := chatSourceID(value)
			n, err := strconv.ParseInt(id, 10, 64)
			if err != nil || n < 1 {
				return nil, fail(422, "judge_id must be a positive integer")
			}
			in["judge_id"] = strconv.FormatInt(n, 10)
			task := "question"
			if value := input["judge_task"]; value != nil {
				var ok bool
				task, ok = value.(string)
				if !ok {
					return nil, fail(422, "judge_task must be brief or question")
				}
			}
			if task != "brief" && task != "question" {
				return nil, fail(422, "judge_task must be brief or question")
			}
			in["judge_task"] = task
		} else if input["judge_task"] != nil {
			return nil, fail(422, "judge_task requires judge_id")
		}
		for _, field := range []string{"session_id", "conversation_id"} {
			in[field] = nil
			if value := input[field]; value != nil {
				id, ok := value.(string)
				if !ok || len([]rune(id)) > 100 || !chatSessionID.MatchString(strings.TrimSpace(id)) {
					return nil, fail(422, "Invalid "+field)
				}
				in[field] = strings.TrimSpace(id)
			}
		}
		in["include_citations"], in["include_documents"], in["include_case_law"] = true, true, nil
		in["case_law_limit"] = 5
		if value, present := input["case_law_limit"]; present {
			n, err := chatInt(value, 1, 50)
			if err != nil {
				return nil, err
			}
			in["case_law_limit"] = n
		}
		if err := validateDocumentFilter(input); err != nil {
			return nil, err
		}
		in["document_filter"] = nil
		if input["document_filter"] != nil {
			f := M{"document_ids": nil, "folder_paths": nil, "sources": nil, "doc_types": nil, "matter_ids": nil, "search_all": false, "include_subfolders": true}
			for key := range f {
				if value, present := object(input["document_filter"])[key]; present {
					f[key] = value
				}
			}
			in["document_filter"] = f
		}
	}
	for _, field := range []string{"include_citations", "include_documents", "include_case_law"} {
		if strategy && field != "include_case_law" {
			continue
		}
		if value, present := input[field]; present {
			if !strategy && field == "include_case_law" && value == nil {
				continue
			}
			parsed, ok := researchBool(value)
			if !ok {
				return nil, fail(422, field+" must be a boolean")
			}
			in[field] = parsed
		}
	}
	return in, nil
}

// Snapshot the authorized scope, never credentials. Later membership changes
// are checked again when reading sources and atomically when publishing.
func (s *Server) prepareChat(ctx context.Context, user string, in M, strategy bool) error {
	if err := s.prepareJudgeChat(ctx, user, in); err != nil {
		return err
	}
	settings, err := s.userSettings(ctx, user)
	if err != nil {
		return err
	}
	in["_chat_settings"] = settings
	sessionMatter := ""
	if !strategy {
		id := str(in, "session_id")
		in["_chat_new_session"] = id == ""
		if id == "" {
			in["session_id"] = uuid()
		} else {
			session, err := s.getObject(ctx, user, "chat_session", id)
			if err != nil {
				return err
			}
			sessionMatter = str(session, "matter_id")
		}
	}
	in["_chat_session_matter"] = sessionMatter
	personal := false
	if sessionMatter != "" {
		if err := s.db.QueryRowContext(ctx, `SELECT is_personal AND owner_id=$2 FROM cc_matters WHERE id=$1`, sessionMatter, user).Scan(&personal); err != nil {
			return err
		}
	}
	in["_chat_session_personal"] = personal
	filter := object(in["document_filter"])
	if strategy {
		filter = M{}
		if in["document_ids"] != nil {
			filter["document_ids"] = in["document_ids"]
		}
		if folder := str(in, "folder_path"); folder != "" {
			filter["folder_paths"] = []string{folder}
		}
		in["document_filter"] = filter
		delete(in, "document_ids")
	}
	docs := []M{}
	explicit := false
	if strategy || in["include_documents"] == true {
		var ids []string
		var err error
		if strategy {
			ids, _, err = s.resolveResearchScope(ctx, user, M{"document_filter": filter})
		} else {
			ids, err = s.resolveChatScope(ctx, user, in["document_filter"])
		}
		if err != nil {
			return err
		}
		explicit = ids != nil || sessionMatter != "" || in["document_filter"] != nil && filter["search_all"] != true
		scope := M{}
		if ids != nil {
			scope["document_ids"] = ids
		}
		docs, err = s.scopedDocuments(ctx, user, scope)
		if err != nil {
			return err
		}
	}
	ids, revisions := []string{}, M{}
	for _, doc := range docs {
		// Shared history is readable by other members. Private or cross-matter
		// files may never be smuggled into it through an explicit filter.
		if !chatSessionAllowsDoc(sessionMatter, personal, user, doc) {
			continue
		}
		id := str(doc, "id")
		ids = append(ids, id)
		revisions[id] = str(doc, "memory_id")
	}
	in["_chat_scope"] = M{"ids": ids, "explicit": explicit, "revisions": revisions}
	return nil
}

func (s *Server) chatRequest(q request) (any, error) {
	raw, err := body(q)
	if err != nil {
		return nil, err
	}
	strategy := strings.HasSuffix(q.r.URL.Path, "/strategy/brief")
	in, err := validateChatRequest(raw, strategy)
	if err != nil {
		return nil, err
	}
	async := false
	if values, present := q.r.URL.Query()["async"]; present {
		var ok bool
		async, ok = researchBool(values[len(values)-1])
		if !ok {
			return nil, fail(422, "async must be a boolean")
		}
	}
	if err = s.prepareChat(q.r.Context(), q.user.ID, in, strategy); err != nil {
		return nil, err
	}
	kind := "chat.reply"
	if strategy {
		kind = "strategy.brief"
	}
	id, err := s.enqueueTask(q.r.Context(), q.user.ID, kind, in)
	if err != nil {
		return nil, err
	}
	if async {
		writeJSON(q.w, 202, M{"job_id": id, "poll_url": "/api/v1/jobs/" + id})
		return nil, nil
	}
	return s.awaitChat(q.r.Context(), q.user.ID, id)
}

func chatSessionAllowsDoc(matter string, personal bool, user string, doc M) bool {
	return matter == "" || str(doc, "matter_id") == matter || personal && str(doc, "matter_id") == "" && str(doc, "user_id") == user
}

func (s *Server) awaitChat(ctx context.Context, user, id string) (M, error) {
	defer func() {
		if ctx.Err() == nil {
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = s.db.ExecContext(cleanup, `UPDATE cc_jobs SET status='cancelled',error='The synchronous request was disconnected',updated_at=now() WHERE id=$1 AND user_id=$2 AND status IN ('pending','running')`, id, user)
	}()
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		var status string
		var data, progress []byte
		var message *string
		err := s.db.QueryRowContext(ctx, `SELECT status,result,error,progress FROM cc_jobs WHERE id=$1 AND user_id=$2`, id, user).Scan(&status, &data, &message, &progress)
		if err != nil {
			return nil, err
		}
		switch status {
		case "completed":
			out := M{}
			if err := json.Unmarshal(data, &out); err != nil {
				return nil, err
			}
			if err := s.resultAccess(ctx, user, out); err != nil {
				return nil, err
			}
			return out, nil
		case "failed", "cancelled":
			detail := "The answer could not be completed."
			if message != nil {
				detail = *message
			}
			p := M{}
			_ = json.Unmarshal(progress, &p)
			code := number(p, "error_status", 502)
			if status == "cancelled" {
				code = 409
			}
			return nil, fail(code, detail)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Server) resolveChatScope(ctx context.Context, user string, value any) ([]string, error) {
	f := object(value)
	if value == nil || f["search_all"] == true {
		return nil, nil
	}
	matters := stringsOf(f["matter_ids"])
	ids := []string{}
	if len(matters) > 0 {
		var err error
		ids, _, err = s.resolveResearchScope(ctx, user, M{"document_filter": M{"matter_ids": matters}})
		if err != nil {
			return nil, err
		}
		if len(stringsOf(f["document_ids"])) == 0 && len(stringsOf(f["folder_paths"])) == 0 {
			return ids, nil
		}
	}
	picked := M{}
	for _, key := range []string{"document_ids", "folder_paths", "sources", "doc_types", "include_subfolders"} {
		picked[key] = f[key]
	}
	if picked["include_subfolders"] == nil {
		delete(picked, "include_subfolders")
	}
	more, _, err := s.resolveResearchScope(ctx, user, M{"document_filter": picked})
	if err != nil {
		return nil, err
	}
	if more == nil {
		return nil, nil
	} // every accessible indexed document
	return uniqueWorkflowStrings(append(ids, more...)), nil
}

func (s *Server) chatRoutes() {
	s.api("POST /api/v1/chat", false, s.chatRequest)
	s.api("POST /api/v1/chat/export", false, s.exportConversation)
	s.api("POST /api/v1/strategy/brief", false, s.chatRequest)
}
