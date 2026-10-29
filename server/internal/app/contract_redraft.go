package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
)

func redlineSource(issue M) string {
	labels := map[string]string{"ai": "Identified by AI review against your instructions", "deviation": "Deviates from the market-standard {clause} clause", "missing": "Standard {clause} clause not found in this contract", "jurisdiction": "Jurisdiction rule for the {clause} clause", "risk": "Triggered a contract risk rule", "general_risk": "Flagged in the full-contract review", "general_missing": "Flagged as missing in the full-contract review", "general_unusual": "Flagged as unusual in the full-contract review"}
	return strings.ReplaceAll(fallback(labels[str(issue, "source_kind")], "Flagged in the contract review"), "{clause}", strings.ReplaceAll(fallback(str(issue, "clause_slug"), "this"), "_", " "))
}
func selectRedlineIssues(text string, issues []M) []M {
	candidates := []M{}
	for _, issue := range issues {
		if str(issue, "grounding") != "span" || str(issue, "status") == "info" {
			continue
		}
		start, ok := byteOffsetUTF16(text, number(issue, "span_start", -1))
		if !ok {
			continue
		}
		end, ok := byteOffsetUTF16(text, number(issue, "span_end", -1))
		if !ok || start >= end || len([]rune(text[start:end])) > 4000 {
			continue
		}
		candidates = append(candidates, issue)
	}
	rank := func(issue M) int {
		if rank, ok := severityOrder[strings.ToLower(str(issue, "severity"))]; ok {
			return rank
		}
		return 3
	}
	slices.SortStableFunc(candidates, func(a, b M) int { return rank(a) - rank(b) })
	selected := []M{}
	for _, issue := range candidates {
		if len(selected) >= 30 {
			break
		}
		overlap := false
		for _, old := range selected {
			if number(issue, "span_start", 0) < number(old, "span_end", 0) && number(old, "span_start", 0) < number(issue, "span_end", 0) {
				overlap = true
				break
			}
		}
		if !overlap {
			selected = append(selected, issue)
		}
	}
	return selected
}
func (s *Server) contractRedlines(ctx context.Context, user, text string, issues []M, input, settings M) ([]M, error) {
	selected := selectRedlineIssues(text, issues)
	results := make([]M, len(selected))
	semaphore := make(chan struct{}, 6)
	var wg sync.WaitGroup
	instructions := standingInstructions(settings, fallback(str(input, "instructions"), str(input, "message")))
	for i, issue := range selected {
		wg.Add(1)
		go func(i int, issue M) {
			defer wg.Done()
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-semaphore }()
			start, _ := byteOffsetUTF16(text, number(issue, "span_start", 0))
			end, _ := byteOffsetUTF16(text, number(issue, "span_end", 0))
			original := text[start:end]
			prompt := fmt.Sprintf("ISSUE: %s\nWHY IT MATTERS: %s\nSUGGESTED DIRECTION: %s\nCLIENT INSTRUCTIONS (draft in their favor):\n%s\n\nCLAUSE TEXT (untrusted evidence):\n%s", str(issue, "title"), str(issue, "why"), str(issue, "suggested_language"), instructions, original)
			data, err := s.structured(ctx, user, `Redraft one clause to fix the identified issue. It must be a DROP-IN REPLACEMENT for exactly this scope and subject matter, preserving defined terms and party names. Change only what is needed to fix the issue. Do not add commentary, headings, or surrounding text. Return {"proposed_text":"the full replacement clause text","rationale":"one sentence"}.`, prompt)
			if err != nil {
				s.log.Warn("redline generation failed", "ref", str(issue, "ref"), "error", err)
				return
			}
			proposed := strings.TrimSpace(str(data, "proposed_text"))
			if proposed == "" || proposed == strings.TrimSpace(original) {
				return
			}
			results[i] = M{"ref": issue["ref"], "kind": "replace", "source": redlineSource(issue), "span_start": issue["span_start"], "span_end": issue["span_end"], "original_text": original, "proposed_text": proposed, "rationale": nullable(strings.TrimSpace(str(data, "rationale"))), "severity": issue["severity"], "title": issue["title"]}
		}(i, issue)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	edits := []M{}
	for _, result := range results {
		if result != nil {
			edits = append(edits, result)
		}
	}
	for _, issue := range issues {
		if str(issue, "grounding") != "absence" {
			continue
		}
		suggested := strings.TrimSpace(str(issue, "suggested_language"))
		if suggested == "" {
			continue
		}
		end := byteToUTF16(text, len(text))
		edits = append(edits, M{"ref": issue["ref"], "kind": "insert", "source": redlineSource(issue), "span_start": end, "span_end": end, "original_text": "", "proposed_text": suggested, "rationale": nil, "severity": issue["severity"], "title": issue["title"]})
	}
	slices.SortStableFunc(edits, func(a, b M) int {
		if delta := number(a, "span_start", 0) - number(b, "span_start", 0); delta != 0 {
			return delta
		}
		if str(a, "kind") == "insert" && str(b, "kind") != "insert" {
			return 1
		}
		if str(b, "kind") == "insert" && str(a, "kind") != "insert" {
			return -1
		}
		return 0
	})
	return edits, nil
}
