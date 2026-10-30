package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestChatFastAndDeepCaseResearchKeepOpinionIdentityAndEvidence(t *testing.T) {
	f := newFixture(t)
	token := f.admin()
	user := str(f.call("GET", "/auth/me", token, nil, 200), "id")
	quote := "Notice must be served before termination when the contract requires notice."
	var mu sync.Mutex
	reads := map[string]int{}
	court := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/search/" {
			writeJSON(w, 200, M{"results": []M{
				{"id": 1001, "caseName": "Alpha v. Beta", "citation": []string{"123 F.3d 456"}, "opinions": []M{{"id": 111}}, "snippet": "A dispute about notice."},
				{"id": 1002, "caseName": "Gamma v. Delta", "opinions": []M{{"id": 222}}, "snippet": "A maritime accident."},
				{"id": 999, "caseName": "Unresolved cluster"},
			}})
			return
		}
		mu.Lock()
		reads[r.URL.Path]++
		mu.Unlock()
		switch r.URL.Path {
		case "/opinions/111/":
			writeJSON(w, 200, M{"plain_text": strings.Repeat("Procedural background and caption. ", 100) + quote})
		case "/opinions/222/":
			writeJSON(w, 200, M{"plain_text": "A maritime accident unrelated to contracts."})
		default:
			t.Error("fetched cluster id as opinion", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(court.Close)
	f.s.cfg.CourtListenerURL, f.s.cfg.CourtListenerToken = court.URL, "court-test"
	var reject atomic.Bool
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reject.Load() {
			w.WriteHeader(400)
			return
		}
		var in M
		_ = json.NewDecoder(r.Body).Decode(&in)
		messages := maps(in["messages"])
		system, prompt := str(messages[0], "content"), str(messages[1], "content")
		var out M
		switch {
		case strings.Contains(system, "screening case-law search"):
			if !strings.Contains(prompt, quote) {
				t.Error("fast excerpt did not reach the notice provision")
			}
			out = M{"cases": []M{{"i": 0, "relevant": true, "confidence": .8, "summary": "Alpha v. Beta requires notice."}, {"i": 1, "relevant": "false"}, {"i": 0.5, "relevant": false}, {"i": 999, "relevant": true}}}
		case strings.Contains(system, "Break the request into"):
			out = M{"targets": []M{{"proposition": "Notice before termination", "query": "notice termination"}, {"proposition": "Contract interpretation", "query": "contract interpretation"}}}
		case strings.Contains(system, "selecting legal authority"):
			if !strings.Contains(prompt, "Notice before termination") || !strings.Contains(prompt, "Contract interpretation") {
				t.Error("overlapping candidate lost a proposition")
			}
			out = M{"relevant": strings.Contains(prompt, quote), "quote": quote, "why": "Requires the agreed notice.", "relevance": .9}
		default:
			t.Error("unexpected authority call", system)
		}
		writeJSON(w, 200, M{"choices": []M{{"message": M{"content": string(jsonBytes(out))}, "finish_reason": "stop"}}})
	}))
	t.Cleanup(model.Close)
	f.s.cfg.LLMURL = model.URL
	question := "Is notice required before termination?"
	results, notes, err := f.s.searchChatCases(context.Background(), user, question, "notice termination", 5)
	if err != nil || len(notes) != 0 || len(results) != 1 {
		t.Fatal(results, notes, err)
	}
	citations := chatCaseCitations(results, "Alpha v. Beta requires notice.", question, 0)
	if citations[0]["opinion_id"] != "111" || citations[0]["cluster_id"] != "1001" || citations[0]["verified"] != nil || citations[0]["was_cited_by_ai"] != true {
		t.Fatal(citations)
	}
	mu.Lock()
	reads = map[string]int{}
	mu.Unlock()
	results, status, err := f.s.researchChatAuthorities(context.Background(), user, question, "notice", 5)
	if err != nil || len(results) != 1 || number(status, "attached", 0) != 1 || number(status, "concepts", 0) != 2 {
		t.Fatal(results, status, err)
	}
	citations = chatCaseCitations(results, "123 F.3d 456", question, 0)
	if citations[0]["verified"] != true || citations[0]["passage"] != quote || citations[0]["was_cited_by_ai"] != true {
		t.Fatal(citations)
	}
	mu.Lock()
	if reads["/opinions/111/"] != 1 || reads["/opinions/222/"] != 1 {
		t.Error("candidate read twice", reads)
	}
	mu.Unlock()
	reject.Store(true)
	results, notes, err = f.s.searchChatCases(context.Background(), user, question, "notice", 5)
	if err != nil || len(results) != 2 || len(notes) != 1 {
		t.Fatal(results, notes, err)
	}
	citations = chatCaseCitations(results, "", question, 0)
	if citations[0]["verified"] != nil || !strings.Contains(str(object(citations[0]["logic"]), "application"), "not screened") {
		t.Fatal(citations)
	}
	if _, _, err = f.s.searchChatCases(context.Background(), user, "notice court:not-a-real-court", "notice", 5); err == nil {
		t.Fatal("invalid court filter was hidden")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err = f.s.searchChatCases(ctx, user, question, "notice", 5); err != context.Canceled {
		t.Fatal(err)
	}
}
