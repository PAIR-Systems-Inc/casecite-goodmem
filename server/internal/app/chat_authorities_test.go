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

func TestStrategyBriefCoverageVerifiedAuthoritiesAndCachedReads(t *testing.T) {
	f := newFixture(t)
	token := f.admin()
	user := str(f.call("GET", "/auth/me", token, nil, 200), "id")
	first := f.upload(token, "", "first.txt", "Notice requires 30 days after a material breach.")
	second := f.upload(token, "", "second.txt", "Notice requires 90 days before termination.")
	var mu sync.Mutex
	reads := map[string]int{}
	quote := "A material breach must precede termination when the agreement expressly requires it."
	court := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token court-test" {
			t.Error("missing CourtListener key")
		}
		if r.URL.Path == "/search/" {
			if r.URL.Query().Get("court") != "ca9" || r.URL.Query().Get("filed_after") != "2020-01-01" || r.URL.Query().Has("cited_gt") {
				t.Error("court/date filters not propagated", r.URL.RawQuery)
			}
			writeJSON(w, 200, M{"results": []M{
				{"id": 1001, "cluster_id": 1001, "caseName": "Alpha v. Beta", "citation": []string{"123 F.3d 456"}, "absolute_url": "/opinion/1001/alpha/", "opinions": []M{{"id": 111}}},
				{"id": 1002, "cluster_id": 1002, "caseName": "Gamma v. Delta", "opinions": []M{{"id": 222}}},
				{"id": 1003, "cluster_id": 1003, "caseName": "Omega v. Theta", "opinions": []M{{"id": 333}}},
				{"id": 999, "cluster_id": 999, "caseName": "Cluster Only"},
			}})
			return
		}
		mu.Lock()
		reads[r.URL.Path]++
		mu.Unlock()
		switch r.URL.Path {
		case "/opinions/111/":
			writeJSON(w, 200, M{"plain_text": strings.Repeat("Background facts. ", 5000) + " SUPPORTING_HOLDING " + quote})
		case "/opinions/222/":
			writeJSON(w, 200, M{"plain_text": "UNSUPPORTIVE_OPINION The agreement allowed unconditional termination."})
		case "/opinions/333/":
			writeJSON(w, 200, M{"plain_text": "INVENTED_QUOTE_TEST " + quote})
		default:
			t.Error("cluster ID must never be fetched as an opinion", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(court.Close)
	f.s.cfg.CourtListenerURL = court.URL
	f.s.cfg.CourtListenerToken = "court-test"
	var active, peak atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		var in M
		_ = json.NewDecoder(r.Body).Decode(&in)
		messages := maps(in["messages"])
		system, prompt := str(messages[0], "content"), str(messages[1], "content")
		payload := M{}
		switch {
		case strings.Contains(system, "senior litigation strategist"):
			if !strings.Contains(prompt, "first.txt") || !strings.Contains(prompt, "second.txt") {
				t.Error("strategy omitted a scoped file", prompt)
			}
			payload = M{"position": "The notice condition matters. Invented v. Fiction, 999 U.S. 999, governs.", "strengths": []M{{"point": "A breach is required.", "refs": []string{"d0", "unknown"}}}, "weaknesses": []M{{"point": "Notice may be late.", "refs": []string{"d1"}}, {"point": "Missing source.", "refs": []string{"invented"}}}, "next_steps": []M{{"step": "Read the notice.", "refs": []string{"d0"}}}}
		case strings.Contains(system, "For each numbered litigation point"):
			payload = M{"targets": []M{{"proposition": "Breach precedes termination.", "query": "contract breach termination"}, {"proposition": "Notice conditions apply.", "query": "contract notice requirements"}, {"proposition": "Consider support.", "query": "contract support"}}}
		case strings.Contains(system, "selecting legal authority for a litigation strategy"):
			payload = M{"supports": false}
			if strings.Contains(prompt, "SUPPORTING_HOLDING") {
				payload = M{"supports": true, "quote": quote, "how": "The holding requires the agreed breach condition."}
			}
			if strings.Contains(prompt, "INVENTED_QUOTE_TEST") {
				payload = M{"supports": true, "quote": quote + " Termination is always unconditional and immediate.", "how": "A fabricated interpretation."}
			}
		default:
			t.Error("unexpected model operation", system)
		}
		writeJSON(w, 200, M{"choices": []M{{"message": M{"content": string(jsonBytes(payload))}, "finish_reason": "stop"}}})
	}))
	t.Cleanup(model.Close)
	f.s.cfg.LLMURL = model.URL
	out, err := f.s.generateStrategyBrief(context.Background(), user, "Assess our position court:ca9 after:2020", []M{first, second}, []string{"/local/"}, true)
	if err != nil {
		t.Fatal(err)
	}
	status := object(out["case_law"])
	if number(status, "attached", 0) != 3 || number(status, "unsupportive", 0) != 3 || number(status, "quote_unverified", 0) != 3 || number(object(out["scope"]), "documents_read", 0) != 2 || len(stringsOf(out["case_law_removed"])) == 0 {
		t.Fatal(out)
	}
	if peak.Load() > 6 {
		t.Fatal("unbounded model concurrency", peak.Load())
	}
	mu.Lock()
	for path, count := range reads {
		if count != 1 {
			t.Error("repeated full-opinion fetch", path, count)
		}
	}
	mu.Unlock()
	for _, citation := range maps(out["citations"]) {
		if citation["type"] == "case_law" && (citation["verified"] != true || citation["opinionId"] != "111" || citation["cluster_id"] != "1001" || citation["passage"] != quote) {
			t.Fatal(citation)
		}
	}
	if maps(out["weaknesses"])[1]["support"] != "unsupported" {
		t.Fatal("unknown document ref became supported", out)
	}
}

func TestChatOpinionBudgetAndEarlyStopDisclosePartialReads(t *testing.T) {
	f := newFixture(t)
	token := f.admin()
	user := str(f.call("GET", "/auth/me", token, nil, 200), "id")
	quote := "The express notice condition controls the right to terminate this agreement."
	court := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, M{"plain_text": quote + strings.Repeat(" Additional discussion.", 5000)})
	}))
	t.Cleanup(court.Close)
	f.s.cfg.CourtListenerURL = court.URL
	var endorse atomic.Bool
	endorse.Store(true)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := M{"relevant": endorse.Load(), "quote": quote}
		writeJSON(w, 200, M{"choices": []M{{"message": M{"content": string(jsonBytes(payload))}, "finish_reason": "stop"}}})
	}))
	t.Cleanup(model.Close)
	f.s.cfg.LLMURL = model.URL
	f.s.cfg.ResearchOpinionWindows = 1
	reader := newChatCourtReader(f.s, user)
	opinion := M{"id": "7", "case_name": "Example v. Sample"}
	v := reader.judge(context.Background(), opinion, chatAuthorityInstructions, "Notice?", "Notice controls.", "relevant")
	if v.Reason != "attached" || !v.Partial || v.WindowsRead != 1 || v.WindowsTotal != 2 {
		t.Fatal(v)
	}
	endorse.Store(false)
	v = reader.judge(context.Background(), opinion, chatAuthorityInstructions, "Notice?", "Notice controls.", "relevant")
	if v.Reason != "unsupportive" || !v.Partial || v.WindowsRead != 1 {
		t.Fatal(v)
	}
	f.s.cfg.ResearchOpinionWindows = 6
	endorse.Store(true)
	v = newChatCourtReader(f.s, user).judge(context.Background(), opinion, chatAuthorityInstructions, "Notice?", "Notice controls.", "relevant")
	if v.Reason != "attached" || !v.Partial {
		t.Fatal("early success was incorrectly labeled a full read", v)
	}
}
