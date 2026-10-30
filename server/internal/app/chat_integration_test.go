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

func chatFixture(t *testing.T, hook func(M, string, string)) *fixture {
	f := newFixture(t)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in M
		_ = json.NewDecoder(r.Body).Decode(&in)
		messages := maps(in["messages"])
		system, prompt := str(messages[0], "content"), str(messages[1], "content")
		if hook != nil {
			hook(in, system, prompt)
		}
		answer := "Notice is 30 days, per agreement.txt."
		var out M
		switch {
		case strings.HasPrefix(system, "Classify the user's message"):
			intent := "ask"
			if strings.Contains(prompt, "Make a strategy") {
				intent = "strategy"
			}
			if strings.Contains(prompt, "Find case law") {
				intent = "research"
			}
			out = M{"intent": intent, "use_case_law": false, "search_query": "notice", "per_file": strings.Contains(prompt, "Compare each file")}
		case strings.Contains(system, "You are grounding"):
			out = M{"claims": []M{{"claim": "Notice is 30 days.", "answer_quote": "Notice is 30 days", "file_ref": "f0", "quote": "Notice is 30 days."}}}
		case strings.Contains(system, "You check statements"):
			out = M{"items": []M{{"n": 0, "label": "supported", "note": "The notice period is explicit."}}}
		case strings.Contains(system, "You are reading ONE document"):
			quote := "Notice is 30 days."
			if strings.Contains(prompt, "Notice is 90 days.") {
				quote = "Notice is 90 days."
			}
			out = M{"answer": quote, "quotes": []M{{"quote": quote, "why": "States the notice period."}}}
		case strings.Contains(system, "You are a senior litigation strategist"):
			out = M{"position": "Notice is required before termination.", "strengths": []M{{"point": "The notice period is explicit.", "refs": []string{"d0"}}}, "weaknesses": []M{{"point": "Compare the other notice requirement.", "refs": []string{"d1"}}}, "next_steps": []M{{"step": "Confirm service of notice.", "refs": []string{}}}}
		case strings.Contains(system, "expert legal document drafter"):
			answer = "NOTICE OF TERMINATION\n[PARTY]\n[DATE]"
		default:
			if in["response_format"] != nil {
				out = M{"citations": []M{}}
			}
		}
		if out != nil {
			answer = string(jsonBytes(out))
		}
		writeJSON(w, 200, M{"choices": []M{{"message": M{"content": answer}, "finish_reason": "stop"}}})
	}))
	t.Cleanup(model.Close)
	f.s.cfg.LLMURL = model.URL
	return f
}

func runChatFixtureJob(t *testing.T, f *fixture, id string) <-chan struct{} {
	t.Helper()
	var user, kind string
	var input []byte
	lease := uuid()
	err := f.s.db.QueryRow(`UPDATE cc_jobs SET status='running',lease_token=$2,lease_until=now()+interval '30 seconds' WHERE id=$1 AND status='pending' RETURNING user_id,kind,input`, id, lease).Scan(&user, &kind, &input)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); f.s.runJob(id, user, kind, lease, input) }()
	return done
}

func awaitChatFixture(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("chat job did not finish")
	}
}

func TestChatSyncAsyncHistoryAndStrategyRoutes(t *testing.T) {
	f := chatFixture(t, nil)
	token := f.admin()
	doc := f.upload(token, "", "agreement.txt", "Notice is 30 days.")
	second := f.upload(token, "", "second.txt", "Notice is 90 days.")
	f.s.StartWorkers()
	out := f.call("POST", "/chat", token, M{"query": "What notice is required?", "document_filter": M{"document_ids": []string{str(doc, "id")}}, "include_case_law": false}, 200)
	if str(out, "session_id") == "" || str(out, "id") == "" || out["mode"] != "research" || len(maps(out["citations"])) != 1 {
		t.Fatal(out)
	}
	if maps(out["citations"])[0]["verified"] != true || maps(out["citations"])[0]["support"] != "supported" {
		t.Fatal(out)
	}
	id := str(out, "session_id")
	session := f.call("GET", "/chat/sessions/"+id, token, nil, 200)
	messages := maps(session["messages"])
	if number(session, "message_count", 0) != 2 || len(messages) != 2 || messages[0]["role"] != "user" || messages[1]["id"] != out["id"] {
		t.Fatal(session)
	}
	job := f.call("POST", "/chat?async=true", token, M{"query": "Compare each file's notice period", "session_id": id, "document_filter": M{"document_ids": []string{str(doc, "id"), str(second, "id")}}, "include_case_law": false, "include_citations": false}, 202)
	deadline := time.Now().Add(15 * time.Second)
	for {
		state := f.call("GET", "/jobs/"+str(job, "job_id"), token, nil, 200)
		if state["status"] == "completed" {
			answer := object(state["result"])
			if object(answer["stats"])["query_intent"] != "per_file" || len(maps(answer["citations"])) != 0 || answer["session_id"] != id {
				t.Fatal(answer)
			}
			break
		}
		if state["status"] == "failed" || time.Now().After(deadline) {
			t.Fatal(state)
		}
		time.Sleep(25 * time.Millisecond)
	}
	session = f.call("GET", "/chat/sessions/"+id, token, nil, 200)
	if number(session, "message_count", 0) != 4 {
		t.Fatal(session)
	}
	brief := f.call("POST", "/strategy/brief", token, M{"question": "What are our strengths?", "document_ids": []string{str(doc, "id"), str(second, "id")}}, 200)
	if number(object(brief["scope"]), "documents_read", 0) != 2 || len(maps(brief["citations"])) != 2 || maps(brief["next_steps"])[0]["support"] != "unsupported" {
		t.Fatal(brief)
	}
	strategy := f.call("POST", "/chat", token, M{"query": "Make a strategy for the matter", "include_case_law": false}, 200)
	if object(strategy["stats"])["query_intent"] != "strategy" || str(object(strategy["strategy"]), "position") == "" {
		t.Fatal(strategy)
	}
}

func TestChatSharedMatterScopeAndHistoryRevocation(t *testing.T) {
	var mu sync.Mutex
	prompts := []string{}
	f := chatFixture(t, func(_ M, _, prompt string) { mu.Lock(); prompts = append(prompts, prompt); mu.Unlock() })
	owner := f.admin()
	member := f.account(owner, "member@example.test")
	memberID := str(f.call("GET", "/auth/me", member, nil, 200), "id")
	matter := f.call("POST", "/matters", owner, M{"name": "Shared matter"}, 200)
	mid := str(matter, "id")
	f.call("POST", "/matters/"+mid+"/members", owner, M{"email": "member@example.test"}, 200)
	doc := f.upload(owner, mid, "agreement.txt", "Notice is 30 days.")
	private := f.upload(owner, "", "private.txt", "PRIVATE_SENTINEL Trade secret.")
	session := f.call("POST", "/chat/sessions", owner, M{"title": "Matter discussion", "matter_id": mid}, 200)
	f.s.StartWorkers()
	out := f.call("POST", "/chat", owner, M{"query": "What notice is required?", "session_id": session["id"], "include_case_law": false, "document_filter": M{"document_ids": []string{str(doc, "id"), str(private, "id")}}}, 200)
	if len(stringsOf(out["access_document_ids"])) != 1 || stringsOf(out["access_document_ids"])[0] != doc["id"] {
		t.Fatal(out)
	}
	f.call("GET", "/chat/sessions/"+str(session, "id"), member, nil, 200)
	personal := f.call("POST", "/chat", member, M{"query": "What notice is required?", "include_case_law": false, "document_filter": M{"document_ids": []string{str(doc, "id")}}}, 200)
	f.call("DELETE", "/matters/"+mid+"/members/"+memberID, owner, nil, 200)
	f.call("GET", "/chat/sessions/"+str(personal, "session_id"), member, nil, 403)
	mu.Lock()
	defer mu.Unlock()
	for _, prompt := range prompts {
		if strings.Contains(prompt, "PRIVATE_SENTINEL") || strings.Contains(prompt, str(private, "id")) {
			t.Fatal("private document reached shared chat")
		}
	}
}

func TestChatResearchDoesNotReadSelectedFilesAndSnapshotsSettings(t *testing.T) {
	var mu sync.Mutex
	models := []string{}
	f := chatFixture(t, func(in M, system, prompt string) {
		mu.Lock()
		models = append(models, str(in, "model"))
		mu.Unlock()
		if strings.Contains(prompt, "PRIVATE_SENTINEL") {
			t.Error("research read private documents")
		}
		if strings.Contains(system, "expert legal document drafter") && (number(in, "max_tokens", 0) != 256 || judgeNumber(in, "temperature") != 0.3) {
			t.Error("draft settings not honored", in)
		}
	})
	token := f.admin()
	doc := f.upload(token, "", "private.txt", "PRIVATE_SENTINEL Trade secret.")
	f.call("PUT", "/settings/rag", token, M{"llm_model": "captured-model", "max_tokens": 256, "temperature": 0.1}, 200)
	job := f.call("POST", "/chat?async=true", token, M{"query": "Find case law on notice, and draft a document", "include_case_law": false, "document_filter": M{"document_ids": []string{str(doc, "id")}}}, 202)
	f.call("DELETE", "/documents/"+str(doc, "id"), token, nil, 200)
	f.call("PUT", "/settings/rag", token, M{"llm_model": "changed-model"}, 200)
	awaitChatFixture(t, runChatFixtureJob(t, f, str(job, "job_id")))
	state := f.call("GET", "/jobs/"+str(job, "job_id"), token, nil, 200)
	if state["status"] != "completed" || len(stringsOf(object(state["result"])["access_document_ids"])) != 0 || !strings.Contains(str(object(state["result"]), "content"), "NOTICE OF TERMINATION") {
		t.Fatal(state)
	}
	f.mu.Lock()
	searched := len(f.retrieved)
	f.mu.Unlock()
	if searched != 0 {
		t.Fatal("research queried GoodMem", searched)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, model := range models {
		if model != "captured-model" {
			t.Fatal("settings changed during queued request", models)
		}
	}
}

func TestChatCancellationDeletionAndRevisionAreAtomic(t *testing.T) {
	for _, action := range []string{"cancel", "delete", "revision"} {
		t.Run(action, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			f := chatFixture(t, func(in M, _, _ string) {
				if in["response_format"] == nil {
					once.Do(func() { close(entered); <-release })
				}
			})
			token := f.admin()
			doc := f.upload(token, "", "agreement.txt", "Notice is 30 days.")
			session := f.call("POST", "/chat/sessions", token, M{"title": "Do not resurrect"}, 200)
			request := M{"query": "What notice is required?", "include_case_law": false, "document_filter": M{"document_ids": []string{str(doc, "id")}}}
			if action == "delete" {
				request["session_id"] = session["id"]
			}
			job := f.call("POST", "/chat?async=true", token, request, 202)
			done := runChatFixtureJob(t, f, str(job, "job_id"))
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				close(release)
				t.Fatal("model did not start")
			}
			switch action {
			case "cancel":
				f.call("DELETE", "/jobs/"+str(job, "job_id"), token, nil, 200)
			case "delete":
				f.call("DELETE", "/chat/sessions/"+str(session, "id"), token, nil, 200)
			case "revision":
				_, err := f.s.db.Exec(`UPDATE cc_objects SET data=jsonb_set(data,'{memory_id}','"changed"') WHERE kind='document' AND id=$1`, doc["id"])
				if err != nil {
					close(release)
					t.Fatal(err)
				}
			}
			close(release)
			awaitChatFixture(t, done)
			var count int
			if err := f.s.db.QueryRow(`SELECT count(*) FROM cc_chat_messages`).Scan(&count); err != nil || count != 0 {
				t.Fatal("partial exchange published", count, err)
			}
			if action == "delete" {
				f.call("GET", "/jobs/"+str(job, "job_id"), token, nil, 404)
				f.call("GET", "/chat/sessions/"+str(session, "id"), token, nil, 404)
			} else {
				state := f.call("GET", "/jobs/"+str(job, "job_id"), token, nil, 200)
				want := "cancelled"
				if action == "revision" {
					want = "failed"
				}
				if state["status"] != want || state["result"] != nil {
					t.Fatal(state)
				}
			}
		})
	}
}

func TestChatEmptyExplicitScopeAndQueueLimit(t *testing.T) {
	f := chatFixture(t, nil)
	token := f.admin()
	f.upload(token, "", "agreement.txt", "Notice is 30 days.")
	for i := 0; i < 5; i++ {
		f.call("POST", "/chat?async=true", token, M{"query": "What notice?", "document_filter": M{"document_ids": []string{}}}, 202)
	}
	f.call("POST", "/chat?async=true", token, M{"query": "Sixth query"}, 429)
	var id string
	if err := f.s.db.QueryRow(`SELECT id FROM cc_jobs ORDER BY created_at LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	awaitChatFixture(t, runChatFixtureJob(t, f, id))
	state := f.call("GET", "/jobs/"+id, token, nil, 200)
	if state["status"] != "completed" || !strings.Contains(str(object(state["result"]), "content"), "no indexed documents in the selected scope") {
		t.Fatal(state)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.retrieved) != 0 {
		t.Fatal("empty scope searched the library")
	}
}

func TestChatDisconnectedSynchronousWaitCancelsQueuedJob(t *testing.T) {
	f := chatFixture(t, nil)
	token := f.admin()
	user := str(f.call("GET", "/auth/me", token, nil, 200), "id")
	id, err := f.s.enqueueTask(context.Background(), user, "chat.reply", M{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = f.s.awaitChat(ctx, user, id); err == nil {
		t.Fatal("cancelled request succeeded")
	}
	state := f.call("GET", "/jobs/"+id, token, nil, 200)
	if state["status"] != "cancelled" {
		t.Fatal(state)
	}
}
