// Test-only deterministic OpenAI-compatible provider. Never use for real work:
// its vectors and answers are fixtures, not semantic embeddings or model output.
package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"
)

func main() {
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
		answer := "The agreement requires 30 days of notice. [1]\n\nThis is a deterministic test response."
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
func write(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
