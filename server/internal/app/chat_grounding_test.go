package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestChatGroundingQuoteAndSupportAreIndependent(t *testing.T) {
	f := newFixture(t)
	token := f.admin()
	user := str(f.call("GET", "/auth/me", token, nil, 200), "id")
	text := "😀 Agreement\nEither party may terminate on 30 days notice, only after an uncured material breach.\nPayment is due monthly."
	doc := f.upload(token, "", "agreement.txt", text)
	var calls atomic.Int32
	var omit atomic.Bool
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var in M
		_ = json.NewDecoder(r.Body).Decode(&in)
		messages := maps(in["messages"])
		system, prompt := str(messages[0], "content"), str(messages[1], "content")
		var payload M
		if strings.Contains(system, "You are grounding") {
			payload = M{"claims": []M{
				{"claim": "Either party can terminate without cause.", "answer_quote": "Either party can terminate without cause.", "file_ref": "f0", "quote": "Either party may terminate on 30 days notice"},
				{"claim": "The charge is $9,000.", "answer_quote": "The charge is $9,000.", "file_ref": "f0", "quote": "The charge is $9,000 payable immediately."},
				{"claim": "State law requires five days.", "file_ref": nil, "quote": ""},
			}}
		} else if strings.Contains(system, "You check statements") {
			if !strings.Contains(prompt, "uncured material breach") || strings.Contains(prompt, "$9,000") {
				t.Error("support pass must include nearby conditions and exclude invented quotes", prompt)
			}
			payload = M{"items": []M{{"n": 0, "label": "partial", "note": "Termination requires an uncured material breach."}, {"n": 999, "label": "supported"}}}
			if omit.Load() {
				payload = M{"items": []M{}}
			}
		} else {
			t.Error("unexpected model operation", system)
		}
		writeJSON(w, 200, M{"choices": []M{{"message": M{"content": string(jsonBytes(payload))}, "finish_reason": "stop"}}})
	}))
	t.Cleanup(model.Close)
	f.s.cfg.LLMURL = model.URL
	results := []M{{"text": "Either party may terminate on 30 days notice", "similarity": .8, "metadata": M{"document_id": doc["id"]}}}
	answer := "Either party can terminate without cause. The charge is $9,000. State law requires five days."
	grounded, err := f.s.groundChatAnswer(context.Background(), user, "Can we terminate?", answer, results, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(grounded.Citations) != 2 || len(grounded.Flagged) != 3 || !grounded.SupportChecked || calls.Load() != 2 {
		t.Fatal(grounded, calls.Load())
	}
	c := grounded.Citations[0]
	start := strings.Index(text, "Either")
	if c["verified"] != true || c["support"] != "partial" || number(c, "doc_span_start", -1) != byteToUTF16(text, start) || number(c, "doc_span_end", -1) != byteToUTF16(text, start+len(str(c, "passage"))) {
		t.Fatal(c)
	}
	if grounded.Citations[1]["verified"] != false || grounded.Citations[1]["support"] != "unsupported" || !strings.Contains(grounded.Notice(), "uncured material breach") {
		t.Fatal(grounded)
	}
	omit.Store(true)
	grounded, err = f.s.groundChatAnswer(context.Background(), user, "Can we terminate?", answer, results, true)
	if err != nil || grounded.SupportChecked || !strings.Contains(grounded.Notice(), "Support could not be checked for 1") {
		t.Fatal(grounded, err)
	}
	before := calls.Load()
	grounded, err = f.s.groundChatAnswer(context.Background(), user, "Can we terminate?", answer, results, false)
	if err != nil || calls.Load() != before+1 || grounded.Citations[0]["support"] != nil {
		t.Fatal(grounded, err, calls.Load())
	}
}

func TestChatReasoningDoesNotPublishInventedEvidence(t *testing.T) {
	f := newFixture(t)
	token := f.admin()
	user := str(f.call("GET", "/auth/me", token, nil, 200), "id")
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := M{"citations": []M{{"ref": "c0", "application": "The payment date is monthly.", "reasoning": []M{
			{"type": "Fact used", "description": "Payment is monthly.", "evidence": "Payment is due monthly."},
			{"type": "Fact used", "description": "A penalty applies.", "evidence": "A $500 late fee applies."},
		}}, {"ref": "c999", "reasoning": []M{{"description": "invented source"}}}}}
		writeJSON(w, 200, M{"choices": []M{{"message": M{"content": string(jsonBytes(payload))}, "finish_reason": "stop"}}})
	}))
	t.Cleanup(model.Close)
	f.s.cfg.LLMURL = model.URL
	c := chatCitation("contract.txt", "document", "Payment is due monthly.", 1)
	if err := f.s.enrichChatCitations(context.Background(), user, "When is payment due?", "Payment is monthly.", []M{c}); err != nil {
		t.Fatal(err)
	}
	steps := maps(c["reasoning"])
	if len(steps) != 2 || str(steps[0], "evidence") != "Payment is due monthly." || steps[1]["evidence"] != nil || !strings.Contains(str(steps[1], "description"), "could not be verified") {
		t.Fatal(c)
	}
}

func TestPerFileChatReportsDuplicateNamesAndTruncation(t *testing.T) {
	f := newFixture(t)
	token := f.admin()
	user := str(f.call("GET", "/auth/me", token, nil, 200), "id")
	first := f.upload(token, "", "same.txt", "😀 Notice requires 30 days.\n"+strings.Repeat("Additional terms. ", 4000))
	second := f.upload(token, "", "same.txt", "This separate file fails analysis.")
	third := f.upload(token, "", "other.txt", "Notice requires 90 days.")
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
		answer := ""
		if strings.Contains(system, "You are reading ONE document") {
			if strings.Contains(prompt, str(second, "id")) {
				w.WriteHeader(400)
				return
			}
			quote := "Notice requires 90 days."
			if strings.Contains(prompt, str(first, "id")) {
				quote = "Notice requires 30 days."
				if !strings.Contains(prompt, "truncated after 60,000") {
					t.Error("missing truncation disclosure")
				}
			}
			answer = string(jsonBytes(M{"answer": quote, "key_quotes": []M{{"quote": quote, "why": "Required notice period."}}}))
		} else if strings.Contains(system, "A lawyer asked one question") {
			if strings.Contains(prompt, str(second, "id")) {
				t.Error("failed file made it into synthesis")
			}
			answer = "same.txt requires 30 days and other.txt requires 90 days. Madeup v. Imaginary, 999 U.S. 123, supports this."
		} else {
			t.Error("unexpected operation", system)
		}
		writeJSON(w, 200, M{"choices": []M{{"message": M{"content": answer}, "finish_reason": "stop"}}})
	}))
	t.Cleanup(model.Close)
	f.s.cfg.LLMURL = model.URL
	out, err := f.s.perFileChatAnswer(context.Background(), user, "Compare each document.", []M{first, second, third})
	if err != nil {
		t.Fatal(err)
	}
	answer := str(out, "content")
	if !strings.Contains(answer, "Could not analyze: same.txt") || !strings.Contains(answer, "first 60,000") || strings.Contains(answer, "Madeup v. Imaginary") || number(object(out["stats"]), "docs_searched", 0) != 2 || peak.Load() > 4 {
		t.Fatal(out, peak.Load())
	}
	citations := maps(out["citations"])
	if len(citations) != 2 || citations[0]["verified"] != true || !strings.Contains(str(object(citations[0]["logic"]), "matching_criteria"), "first 60,000") || number(citations[0], "doc_span_start", 0) != 3 {
		t.Fatal(citations)
	}
}
