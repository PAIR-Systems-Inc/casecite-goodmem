package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func judgeChatModel(t *testing.T, f *fixture, hook func(string, string)) {
	t.Helper()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in M
		_ = json.NewDecoder(r.Body).Decode(&in)
		messages := maps(in["messages"])
		system, prompt := str(messages[0], "content"), str(messages[1], "content")
		if hook != nil {
			hook(system, prompt)
		}
		answer := "Jane Example attended Example University. Notice v. Example appears in the cached profile. Phantom v. Stranger is invented."
		switch {
		case strings.Contains(system, "You are grounding"):
			claims := []M{{"claim": "Jane Example attended Example University.", "answer_quote": "Jane Example attended Example University.", "file_ref": "judge_profile", "quote": "Example University"}, {"claim": "Notice v. Example appears in the cached profile.", "answer_quote": "Notice v. Example appears in the cached profile.", "file_ref": "judge_profile", "quote": "Notice v. Example"}}
			if strings.Contains(prompt, `"ref":"f0"`) {
				claims = append(claims, M{"claim": "Notice is 30 days.", "answer_quote": "Notice is 30 days.", "file_ref": "f0", "quote": "Notice is 30 days."})
			}
			answer = string(jsonBytes(M{"claims": claims}))
		case strings.Contains(system, "You check statements"):
			answer = string(jsonBytes(M{"items": []M{{"n": 0, "label": "supported"}, {"n": 1, "label": "supported"}, {"n": 2, "label": "supported"}}}))
		case in["response_format"] != nil:
			t.Error("Judge requests must not use the general chat router or discard the document scope", system)
			answer = "{}"
		default:
			if strings.Contains(prompt, "The biography mentions Biography v. Reference") {
				answer += " Biography v. Reference appears in the biographical notes."
			}
			if strings.Contains(prompt, "Notice is 30 days.") {
				answer += " Notice is 30 days."
			}
		}
		writeJSON(w, 200, M{"choices": []M{{"message": M{"content": answer}, "finish_reason": "stop"}}})
	}))
	t.Cleanup(model.Close)
	f.s.cfg.LLMURL = model.URL
}

func buildJudgeChatFixture(t *testing.T) (*fixture, string) {
	t.Helper()
	f, _ := judgeFixture(t, nil)
	token := f.admin()
	job := f.call("POST", "/judge-intel/build/42?async=true", token, nil, 202)
	runReviewJob(t, f, token, str(job, "job_id"))
	return f, token
}

func TestJudgeChatSourceContextAndDocumentGrounding(t *testing.T) {
	f, token := buildJudgeChatFixture(t)
	if _, err := f.s.db.Exec(`UPDATE cc_judge_cache SET profile=jsonb_set(profile,'{wikipedia_notable_cases}',to_jsonb($1::text)) WHERE judge_id='42'`, "The biography mentions Biography v. Reference."); err != nil {
		t.Fatal(err)
	}
	doc := f.upload(token, "", "agreement.txt", "Notice is 30 days.")
	f.upload(token, "", "private.txt", "PRIVATE_SENTINEL")
	judgeChatModel(t, f, func(system, prompt string) {
		if strings.Contains(prompt, "PRIVATE_SENTINEL") || strings.Contains(prompt, "CLIENT_FORGED_PROFILE") {
			t.Error("unauthorized context in judge answer", prompt)
		}
		if strings.Contains(system, "You are analyzing a cached judicial intelligence profile") {
			for _, field := range []string{"Jane Example", "Example University", "Notice v. Example", "Born in Oakland.", `"opinion_stats"`, `"docket_stats"`} {
				if !strings.Contains(prompt, field) {
					t.Errorf("missing original profile context %q", field)
				}
			}
			if !strings.Contains(system, "NOTABLE CASES") && strings.Contains(prompt, "Prepare a brief") {
				t.Error("original seven-section brief instructions missing")
			}
		}
	})
	for _, includeDocs := range []bool{false, true} {
		query := "Prepare a brief"
		task := "brief"
		if includeDocs {
			query, task = "What do my documents add?", "question"
		}
		job := f.call("POST", "/chat?async=true", token, M{"query": query, "judge_id": 42, "judge_task": task, "include_documents": includeDocs, "include_case_law": false, "document_filter": M{"document_ids": []string{str(doc, "id")}}, "judge_profile": M{"name": "CLIENT_FORGED_PROFILE"}, "_judge_version": "forged"}, 202)
		out := runReviewJob(t, f, token, str(job, "job_id"))
		if !strings.Contains(str(out, "content"), "Notice v. Example") || !strings.Contains(str(out, "content"), "Biography v. Reference") || strings.Contains(str(out, "content"), "Phantom v. Stranger") || strings.Contains(str(out, "content"), "not fully supported") {
			t.Fatal(out)
		}
		if str(object(out["judge_context"]), "id") != "42" || str(object(out["judge_context"]), "task") != task {
			t.Fatal(out)
		}
		citations := maps(out["citations"])
		expected := 2
		if includeDocs {
			expected++
		}
		if len(citations) != expected {
			t.Fatal(citations)
		}
		for _, c := range citations[:2] {
			if c["type"] != "judge_profile" || c["document_id"] != nil || c["doc_span_start"] != nil || c["confidence"] != nil || c["verified"] != true || c["support"] != "supported" || str(c, "url") != "https://www.courtlistener.com/person/42/" {
				t.Fatal(c)
			}
		}
		if includeDocs && (citations[2]["document_id"] != doc["id"] || citations[2]["verified"] != true) {
			t.Fatal(citations[2])
		}
		session := f.call("GET", "/chat/sessions/"+str(out, "session_id"), token, nil, 200)
		messages := maps(session["messages"])
		if len(messages) != 2 || messages[0]["content"] != query || str(object(messages[1]["judge_context"]), "id") != "42" {
			t.Fatal(session)
		}
	}
}

func TestJudgeChatAdmissionAndQueuedProfileRevision(t *testing.T) {
	f, token := buildJudgeChatFixture(t)
	for _, fields := range []M{{"judge_id": -1}, {"judge_id": 42.5}, {"judge_id": true}, {"judge_id": ""}, {"judge_id": "42", "judge_task": "draft"}, {"judge_id": 42, "judge_task": true}, {"judge_task": "brief"}} {
		fields["query"] = "Question"
		f.call("POST", "/chat?async=true", token, fields, 422)
	}
	f.call("POST", "/chat?async=true", token, M{"query": "Question", "judge_id": 99}, 404)
	member := f.account(token, "judge-admission@example.test")
	if err := f.s.kvSet(context.Background(), "permissions:attorney", []string{"chat.use"}, 0); err != nil {
		t.Fatal(err)
	}
	f.call("POST", "/chat?async=true", member, M{"query": "Question", "judge_id": 42}, 403)
	job := f.call("POST", "/chat?async=true", token, M{"query": "Question", "judge_id": 42, "include_documents": false}, 202)
	if _, err := f.s.db.Exec(`UPDATE cc_judge_cache SET updated_at=updated_at+interval '1 second' WHERE judge_id='42'`); err != nil {
		t.Fatal(err)
	}
	judgeChatModel(t, f, func(system, prompt string) { t.Error("stale queued profile reached the model") })
	awaitChatFixture(t, runChatFixtureJob(t, f, str(job, "job_id")))
	state := f.call("GET", "/jobs/"+str(job, "job_id"), token, nil, 200)
	if state["status"] != "failed" || !strings.Contains(str(state, "error"), "profile changed") {
		t.Fatal(state)
	}
}

func TestJudgeChatPublicationRechecksProfilePermissionAndCancellation(t *testing.T) {
	for _, action := range []string{"refresh", "delete", "permission", "cancel"} {
		t.Run(action, func(t *testing.T) {
			f, admin := buildJudgeChatFixture(t)
			token := f.account(admin, "judge-answer@example.test")
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			judgeChatModel(t, f, func(system, prompt string) {
				if strings.Contains(system, "You are analyzing a cached judicial intelligence profile") {
					close(entered)
					<-release
				}
			})
			job := f.call("POST", "/chat?async=true", token, M{"query": "Summarize this judge", "judge_id": 42, "include_documents": false, "include_case_law": false}, 202)
			done := runChatFixtureJob(t, f, str(job, "job_id"))
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("answer never started")
			}
			switch action {
			case "refresh":
				// Reserve a new generation while the older published profile stays readable.
				f.call("POST", "/judge-intel/build/42?async=true&force_refresh=true", admin, nil, 202)
			case "delete":
				f.call("DELETE", "/judge-intel/profile/42", admin, nil, 200)
			case "permission":
				if err := f.s.kvSet(context.Background(), "permissions:attorney", []string{"chat.use", "documents.view"}, 0); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				f.call("DELETE", "/jobs/"+str(job, "job_id"), token, nil, 200)
			}
			once.Do(func() { close(release) })
			awaitChatFixture(t, done)
			var count int
			if err := f.s.db.QueryRow(`SELECT count(*) FROM cc_chat_messages`).Scan(&count); err != nil || count != 0 {
				t.Fatal("obsolete answer entered history", count, err)
			}
			var status string
			if err := f.s.db.QueryRow(`SELECT status FROM cc_jobs WHERE id=$1`, job["job_id"]).Scan(&status); err != nil || (status != "failed" && status != "cancelled") {
				t.Fatal(status, err)
			}
		})
	}
}
