// Test-only deterministic OpenAI-compatible provider. Never use for real work:
// its vectors and answers are fixtures, not semantic embeddings or model output.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	// Clio Manage protocol fixture. OAuth consent is simulated in the browser;
	// token exchange, listing and download use this real HTTP server.
	http.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		if r.ParseForm() != nil || r.Form.Get("client_id") != "fixture-clio-client" || r.Form.Get("client_secret") != "fixture-clio-secret" {
			http.Error(w, "Invalid fixture client", 401)
			return
		}
		write(w, map[string]any{"access_token": "fixture-clio-access", "refresh_token": "fixture-clio-refresh", "expires_in": 3600})
	})
	http.HandleFunc("/api/v4/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-clio-access" {
			http.Error(w, "Invalid fixture grant", 401)
			return
		}
		switch r.URL.Path {
		case "/api/v4/users/who_am_i.json":
			write(w, map[string]any{"data": map[string]any{"id": 1, "name": "Clio Fixture", "email": "fixture@example.test"}})
		case "/api/v4/documents.json":
			write(w, map[string]any{"data": []any{map[string]any{"id": 100, "name": "Imported scan", "filename": "clio-scanned-notice.pdf", "content_type": "application/pdf", "updated_at": "2026-10-29T12:00:00Z", "matter": map[string]any{"id": 1, "display_number": "CLIO-1"}}}, "meta": map[string]any{"paging": map[string]any{}}})
		case "/api/v4/documents/100/download.json":
			data, err := os.ReadFile("/fixtures/scanned_notice.pdf")
			if err != nil {
				http.Error(w, "Missing PDF fixture", 500)
				return
			}
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write(data)
		default:
			http.NotFound(w, r)
		}
	})
	// This endpoint is an on-network dots.ocr protocol fixture. GoodMem still
	// decodes the PDF, renders each page, calls OCR and returns page/layout data.
	// The fixed transcription tests that plumbing, not model OCR accuracy.
	http.HandleFunc("/ocr/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Messages []struct {
				Content []struct {
					Type  string `json:"type"`
					Image struct {
						URL string `json:"url"`
					} `json:"image_url"`
				} `json:"content"`
			} `json:"messages"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20)).Decode(&input) != nil {
			http.Error(w, "Invalid multimodal OCR request", 400)
			return
		}
		valid := false
		for _, message := range input.Messages {
			for _, part := range message.Content {
				if part.Type != "image_url" {
					continue
				}
				parts := strings.SplitN(part.Image.URL, ",", 2)
				if len(parts) != 2 || !strings.HasPrefix(parts[0], "data:image/") || !strings.HasSuffix(parts[0], ";base64") {
					continue
				}
				data, err := base64.StdEncoding.DecodeString(parts[1])
				if err != nil {
					continue
				}
				config, _, err := image.DecodeConfig(bytes.NewReader(data))
				valid = err == nil && config.Width > 100 && config.Height > 100
			}
		}
		if !valid {
			http.Error(w, "Expected a real rendered image", 400)
			return
		}
		answer := `[{"bbox":[120,170,1000,215],"category":"Title","text":"SCANNED AGREEMENT"},{"bbox":[120,270,1000,315],"category":"Text","text":"Notice is 30 days."},{"bbox":[120,330,1000,375],"category":"Text","text":"Payment is due monthly."}]`
		write(w, map[string]any{"id": "test-ocr", "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": answer}, "finish_reason": "stop"}}})
	})
	judgeProvider := map[string]any{}
	if raw, err := os.ReadFile("/fixtures/judge.json"); err == nil {
		var fixture map[string]any
		if err = json.Unmarshal(raw, &fixture); err != nil {
			log.Fatal(err)
		}
		judgeProvider = remapJudgeFixture(fixture["provider"]).(map[string]any)
	}
	http.HandleFunc("/wiki", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("list") == "search" {
			write(w, map[string]any{"query": map[string]any{"search": []any{map[string]any{"title": "Jane Example"}}}})
			return
		}
		text := "Jane Example (born 1965) is an American judge."
		if r.URL.Query().Get("exintro") == "" {
			text += "\n== Early life ==\nBorn in Oakland.\n== Education ==\nExample University."
		}
		write(w, map[string]any{"query": map[string]any{"pages": map[string]any{"1": map[string]any{"extract": text}}}})
	})
	// The browser suite calls the Go application's real CourtListener adapter.
	// Its upstream is controlled data from the Python parity oracle, not a live
	// legal database or a browser network interception.
	if raw, err := os.ReadFile("/fixtures/legal_tools.json"); err == nil {
		var fixture struct {
			Provider map[string]map[string]any `json:"provider"`
		}
		if err = json.Unmarshal(raw, &fixture); err != nil {
			log.Fatal(err)
		}
		http.HandleFunc("/api/rest/v4/", func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Token test-court-only" {
				w.WriteHeader(403)
				return
			}
			path := strings.TrimPrefix(r.URL.Path, "/api/rest/v4")
			params := r.URL.Query()
			judgeKey := path
			if path == "/people/" {
				judgeKey = "search_people"
			}
			if path == "/search/" {
				judgeKey = ""
				if strings.Contains(params.Get("q"), "author_id:") {
					judgeKey = "search_opinions"
				}
				if strings.Contains(params.Get("q"), "assigned_to_id:") {
					judgeKey = "search_dockets"
				}
			}
			if data, ok := judgeProvider[judgeKey]; ok {
				write(w, data)
				return
			}
			if path == "/search/" {
				if params.Get("q") == "upstream-failure" {
					w.WriteHeader(503)
					return
				}
				if after := params.Get("filed_after"); len(after) >= 4 {
					year, _ := strconv.Atoi(after[:4])
					write(w, map[string]any{"count": map[int]int{2020: 100, 2021: 120, 2022: 150, 2023: 160}[year]})
					return
				}
				if strings.HasPrefix(params.Get("q"), "cites:") {
					write(w, map[string]any{"results": []any{map[string]any{"cluster_id": 88, "caseName": "Later v. Notice", "citation": []string{"101 U.S. 2"}, "court": "Court of Appeals", "dateFiled": "2023-01-01", "absolute_url": "/opinion/88/later/", "opinions": []any{map[string]any{"id": 88, "snippet": "We followed <mark>100 U.S. 20</mark> on the notice requirement."}}}}})
					return
				}
				key := map[string]string{"o": "search_opinions", "d": "search_dockets", "oa": "search_audio"}[params.Get("type")]
				// Copy before adapting pagination; concurrent requests share fixtures.
				data := map[string]any{}
				for k, v := range fixture.Provider[key] {
					data[k] = v
				}
				if key == "search_opinions" {
					data["count"] = 2
					data["next"] = "http://provider:8080/api/rest/v4/search/?q=notice&type=o&cursor=next"
					if params.Get("cursor") != "" {
						data["next"] = nil
					}
				}
				write(w, data)
				return
			}
			if data, ok := fixture.Provider[path]; ok {
				write(w, data)
				return
			}
			w.WriteHeader(404)
		})
	}
	http.HandleFunc("/v1/embeddings", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Input any `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		texts := []string{}
		switch v := in.Input.(type) {
		case string:
			texts = append(texts, v)
		case []any:
			for _, x := range v {
				texts = append(texts, fmt.Sprint(x))
			}
		}
		data := []any{}
		for i, text := range texts {
			hash := sha256.Sum256([]byte(text))
			vector := make([]float64, 16)
			sum := 0.0
			for j := range vector {
				vector[j] = float64(hash[j]) / 255
				sum += vector[j] * vector[j]
			}
			for j := range vector {
				vector[j] /= math.Sqrt(sum)
			}
			data = append(data, map[string]any{"object": "embedding", "index": i, "embedding": vector})
		}
		write(w, map[string]any{"object": "list", "data": data, "model": "test-embedding", "usage": map[string]int{"prompt_tokens": 1, "total_tokens": 1}})
	})
	http.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
			ResponseFormat any `json:"response_format"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		if len(in.Messages) == 0 {
			http.Error(w, "messages required", 400)
			return
		}
		system, question := in.Messages[0].Content, in.Messages[len(in.Messages)-1].Content
		answer := "The agreement requires 30 days of notice. [1]\n\nThis is a deterministic test response."
		if in.ResponseFormat == nil && strings.Contains(system, "You are analyzing a cached judicial intelligence profile") {
			if !strings.Contains(question, "Example University") || !strings.Contains(question, "Notice v. Example") {
				http.Error(w, "judge profile was not supplied", 400)
				return
			}
			answer = "Jane Example attended Example University. Notice v. Example appears in the cached profile. The supplied data does not establish how this judge would rule."
			if strings.Contains(question, "Untrusted source evidence JSON:") {
				answer += " The agreement requires 30 days of notice. [1]"
			}
			if strings.Contains(system, "QUICK FACTS") {
				answer = "## QUICK FACTS\n- Jane Example attended Example University.\n\n## BACKGROUND\nThe profile includes a biographical summary.\n\n## EDUCATION\nJane Example attended Example University.\n\n## CAREER & APPOINTMENTS\nThe profile lists judicial service.\n\n## JUDICIAL RECORD ANALYSIS\nThis is a limited sample; counts do not predict rulings.\n\n## NOTABLE CASES\nNotice v. Example appears in the cached profile.\n\n## COURTROOM STRATEGY\n- Check the relevant opinion before relying on its holding.\n- Confirm current local rules.\n- Separate record facts from inference.\n- Do not predict rulings from citation counts."
			}
		}
		if in.ResponseFormat == nil && strings.Contains(question, "Pause this answer for the cancellation test") {
			select {
			case <-time.After(3 * time.Second):
			case <-r.Context().Done():
				return
			}
		}
		if in.ResponseFormat != nil {
			docs := []map[string]any{}
			prompt := in.Messages[len(in.Messages)-1].Content
			if i := strings.Index(prompt, "Evidence JSON:\n"); i >= 0 {
				json.Unmarshal([]byte(prompt[i+len("Evidence JSON:\n"):]), &docs)
			}
			out := map[string]any{"answer": "Test review: verify the notice period before termination.", "findings": []any{}}
			if strings.Contains(prompt, "Extract the fields below from the labelled excerpts") {
				out = map[string]any{"fields": map[string]any{
					"notice":        map[string]any{"value": "30 days", "quote": "Notice is 30 days."},
					"liability_cap": map[string]any{"value": "USD 1 million", "quote": "Liability is capped at one million dollars."},
				}}
			}
			if strings.Contains(prompt, "INSTRUCTIONS:\n") {
				out = map[string]any{"title": "Workflow memo", "text": "The agreement requires 30 days of notice. Counsel approved this draft after reviewing the extracted terms."}
			}
			switch {
			case strings.HasPrefix(system, "Classify the user's message"):
				intent := "ask"
				if strings.Contains(prompt, "Make a strategy") {
					intent = "strategy"
				}
				out = map[string]any{"intent": intent, "use_case_law": false, "search_query": "notice", "per_file": strings.Contains(prompt, "Compare each file")}
			case strings.Contains(system, "You are grounding"):
				out = map[string]any{"claims": []any{map[string]any{"claim": "Notice is 30 days.", "answer_quote": "The agreement requires 30 days of notice.", "file_ref": "f0", "quote": "Notice is 30 days."}}}
				if strings.Contains(prompt, `"ref":"judge_profile"`) {
					claims := []any{map[string]any{"claim": "Jane Example attended Example University.", "answer_quote": "Jane Example attended Example University.", "file_ref": "judge_profile", "quote": "Example University"}, map[string]any{"claim": "Notice v. Example appears in the cached profile.", "answer_quote": "Notice v. Example appears in the cached profile.", "file_ref": "judge_profile", "quote": "Notice v. Example"}}
					if strings.Contains(prompt, `"ref":"f0"`) {
						claims = append(claims, map[string]any{"claim": "Notice is 30 days.", "answer_quote": "The agreement requires 30 days of notice.", "file_ref": "f0", "quote": "Notice is 30 days."})
					}
					out = map[string]any{"claims": claims}
				}
			case strings.Contains(system, "You check statements"):
				out = map[string]any{"items": []any{map[string]any{"n": 0, "label": "supported", "note": "The fact appears in the source."}, map[string]any{"n": 1, "label": "supported"}, map[string]any{"n": 2, "label": "supported"}}}
			case strings.Contains(system, "You are reading ONE document"):
				out = map[string]any{"answer": "Notice is 30 days.", "quotes": []any{map[string]any{"quote": "Notice is 30 days.", "why": "States the notice period."}}}
			case strings.Contains(system, "You are a senior litigation strategist"):
				out = map[string]any{"position": "The agreement requires notice before termination.", "strengths": []any{map[string]any{"point": "The notice period is explicit.", "refs": []string{"d0"}}}, "weaknesses": []any{}, "next_steps": []any{map[string]any{"step": "Confirm receipt of the notice.", "refs": []string{}}}}
			case strings.Contains(prompt, "Break it into"):
				out = map[string]any{"sub_questions": []any{
					map[string]any{"question": "What notice is required?", "tools": []string{"documents"}},
					map[string]any{"question": "What payment terms apply?", "tools": []string{"documents"}},
				}}
			case strings.Contains(prompt, "Answer the sub-question using ONLY"):
				out = map[string]any{"answer": "Notice is 30 days. [P1]", "claims": []any{map[string]any{"text": "Notice period", "quote": "Notice is 30 days.", "refs": []string{"P1"}}}}
			case strings.Contains(prompt, "senior legal researcher"):
				out = map[string]any{"answer": "The agreement requires 30 days of notice. [P1]", "key_findings": []any{map[string]any{"text": "The notice period is 30 days.", "refs": []string{"P1"}}}, "open_questions": []string{"When was notice delivered?"}}
			}
			if len(docs) > 0 {
				out["findings"] = []any{map[string]any{"title": "Notice period", "severity": "minor", "document_id": docs[0]["id"], "quote": "Notice is 30 days.", "explanation": "Send notice before the deadline.", "proposed_language": "Notice must be delivered in writing."}}
			}
			if strings.Contains(prompt, "EVERY selected document") {
				rows := []any{}
				fields := []string{}
				if i := strings.Index(prompt, "The fields are: "); i >= 0 {
					part := prompt[i+len("The fields are: "):]
					if end := strings.Index(part, "]."); end >= 0 {
						json.Unmarshal([]byte(part[:end+1]), &fields)
					}
				}
				for _, d := range docs {
					cells := []any{}
					for _, f := range fields {
						cells = append(cells, map[string]any{"field": f, "value": "30 days", "quote": "Notice is 30 days."})
					}
					rows = append(rows, map[string]any{"document_id": d["id"], "cells": cells})
				}
				out["rows"] = rows
			}
			b, _ := json.Marshal(out)
			answer = string(b)
		}
		write(w, map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": answer}, "finish_reason": "stop"}}, "usage": map[string]int{"total_tokens": 100}})
	})
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { write(w, map[string]any{"test_provider": true}) })
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// Distinct browser fixture IDs keep the legal-tool and judge readers independent.
func remapJudgeFixture(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, item := range v {
			out[remapJudgeFixture(k).(string)] = remapJudgeFixture(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = remapJudgeFixture(item)
		}
		return out
	case string:
		return strings.NewReplacer("/77/", "/777/", "/55/", "/555/", "/56/", "/556/").Replace(v)
	case float64:
		if mapped, ok := map[float64]float64{77: 777, 55: 555, 56: 556}[v]; ok {
			return mapped
		}
	}
	return value
}
func write(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
