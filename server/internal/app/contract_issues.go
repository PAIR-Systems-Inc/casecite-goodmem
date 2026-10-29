package app

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var issueStatusByKind = map[string]string{"ai": "unusual", "deviation": "off_market", "missing": "missing", "jurisdiction": "unusual", "risk": "unusual", "general_risk": "unusual", "general_missing": "missing", "general_unusual": "unusual"}
var severityOrder = map[string]int{"critical": 0, "major": 1, "minor": 2}

func issueSeverity(value, def string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "informational" {
		return "minor"
	}
	if _, ok := severityOrder[value]; ok {
		return value
	}
	return def
}

var normKeyRE = regexp.MustCompile(`[^a-z0-9]+`)

func normKey(value string) string {
	return strings.Join(strings.Fields(normKeyRE.ReplaceAllString(strings.ToLower(value), " ")), " ")
}

func collectFindings(clause, general M) []M {
	findings := []M{}
	for i, d := range maps(clause["deviations"]) {
		slug := str(d, "canonical_slug")
		name := slug + ": " + fallback(str(d, "deviation_type"), "deviation")
		if sub := str(d, "sub_element"); sub != "" {
			name += " (" + sub + ")"
		}
		findings = append(findings, M{"ref": fmt.Sprintf("dev-%d", i), "kind": "deviation", "name": name, "severity": issueSeverity(str(d, "severity"), "major"), "matched_text": charSlice(str(d, "matched_text"), 0, 200), "span_start": d["span_start"], "span_end": d["span_end"], "clause_slug": d["canonical_slug"]})
	}
	idx := 0
	for _, bucket := range []struct{ key, sev string }{{"missing_required", "major"}, {"missing_recommended", "minor"}} {
		for _, m := range maps(object(clause["missing"])[bucket.key]) {
			findings = append(findings, M{"ref": fmt.Sprintf("miss-%d", idx), "kind": "missing", "name": "Missing clause: " + fallback(str(m, "name"), str(m, "canonical_slug")), "severity": bucket.sev, "matched_text": "", "span_start": nil, "span_end": nil, "clause_slug": m["canonical_slug"]})
			idx++
		}
	}
	for i, f := range maps(clause["jurisdiction_flags"]) {
		findings = append(findings, M{"ref": fmt.Sprintf("jur-%d", i), "kind": "jurisdiction", "name": charSlice(fallback(str(f, "note"), "Jurisdiction issue: "+str(f, "canonical_slug")), 0, 700), "severity": issueSeverity(str(f, "severity"), "major"), "matched_text": "", "span_start": nil, "span_end": nil, "clause_slug": f["canonical_slug"]})
	}
	keys := []string{}
	for _, f := range findings {
		for _, k := range []string{"clause_slug", "name"} {
			if value := normKey(str(f, k)); value != "" {
				keys = append(keys, value)
			}
		}
	}
	idx = 0
	add := func(kind, name, sev, dedup string) {
		key := normKey(fallback(dedup, name))
		for _, old := range keys {
			if key != "" && (key == old || (len(key) > 3 && (strings.Contains(key, old) || strings.Contains(old, key)))) {
				return
			}
		}
		findings = append(findings, M{"ref": fmt.Sprintf("gen-%d", idx), "kind": kind, "name": charSlice(name, 0, 200), "severity": sev, "matched_text": "", "span_start": nil, "span_end": nil, "clause_slug": nil})
		keys = append(keys, normKey(name))
		idx++
	}
	for _, r := range maps(general["risks"]) {
		name := strings.TrimSpace(fallback(str(r, "description"), str(r, "risk_type")))
		if name == "" {
			continue
		}
		sev := map[string]string{"high": "critical", "medium": "major", "low": "minor"}[strings.ToLower(strings.TrimSpace(str(r, "severity")))]
		add("general_risk", name, fallback(sev, "major"), "")
	}
	for _, m := range maps(general["missing_clauses"]) {
		name := strings.TrimSpace(str(m, "clause"))
		if name == "" {
			continue
		}
		sev := "minor"
		if strings.EqualFold(strings.TrimSpace(str(m, "importance")), "critical") {
			sev = "major"
		}
		add("general_missing", "Missing clause: "+name, sev, name)
	}
	for _, u := range maps(general["unusual_provisions"]) {
		name := strings.TrimSpace(fallback(str(u, "provision"), str(u, "concern")))
		if name != "" {
			add("general_unusual", name, "minor", "")
		}
	}
	return findings
}

func mergeIssues(findings []M, payload M) []M {
	byRef := map[string]M{}
	for _, f := range findings {
		byRef[str(f, "ref")] = f
	}
	seen := map[string]bool{}
	out := []M{}
	mechanical := func(f M) M {
		return M{"ref": f["ref"], "title": f["name"], "status": fallback(issueStatusByKind[str(f, "kind")], "info"), "severity": f["severity"], "why": f["why"], "suggested_language": f["suggested_language"], "span_start": f["span_start"], "span_end": f["span_end"], "clause_slug": f["clause_slug"], "source_kind": f["kind"]}
	}
	for _, entry := range maps(payload["issues"]) {
		ref := str(entry, "ref")
		source := byRef[ref]
		if source == nil || seen[ref] {
			continue
		}
		seen[ref] = true
		item := mechanical(source)
		status := strings.ToLower(strings.TrimSpace(str(entry, "status")))
		if slices.Contains([]string{"off_market", "missing", "unusual", "info"}, status) {
			item["status"] = status
		}
		sev := strings.ToLower(strings.TrimSpace(str(entry, "severity")))
		if _, ok := severityOrder[sev]; ok {
			item["severity"] = sev
		}
		item["title"] = fallback(strings.TrimSpace(str(entry, "title")), str(source, "name"))
		item["why"] = nullable(strings.TrimSpace(str(entry, "why")))
		item["suggested_language"] = nullable(strings.TrimSpace(str(entry, "suggested_language")))
		out = append(out, item)
	}
	for _, f := range findings {
		if !seen[str(f, "ref")] {
			out = append(out, mechanical(f))
		}
	}
	slices.SortStableFunc(out, func(a, b M) int {
		rank := func(m M) int {
			if n, ok := severityOrder[str(m, "severity")]; ok {
				return n
			}
			return 3
		}
		return rank(a) - rank(b)
	})
	return out
}
func isAbsenceClaim(issue M) bool {
	if issue["span_start"] != nil {
		return false
	}
	kind := str(issue, "source_kind")
	if kind != "" {
		return kind == "missing" || kind == "general_missing"
	}
	return strings.HasPrefix(str(issue, "ref"), "miss-")
}

// Match verbatim first, then whitespace-only normalization, like quote_verify.py.
// Returned offsets address the actual original bytes; callers expose UTF-16.
func findQuote(text, quote string) (int, int, bool) {
	if text == "" || quote == "" {
		return 0, 0, false
	}
	if at := strings.Index(text, quote); at >= 0 {
		return at, at + len(quote), true
	}
	words := strings.Fields(quote)
	normalized := strings.Join(words, " ")
	if len([]rune(normalized)) < 12 {
		return 0, 0, false
	}
	for i := range words {
		words[i] = regexp.QuoteMeta(words[i])
	}
	re, err := regexp.Compile(strings.Join(words, `[\s\p{Z}\x{0085}]+`))
	if err != nil {
		return 0, 0, false
	}
	m := re.FindStringIndex(text)
	if m == nil {
		return 0, 0, false
	}
	return m[0], m[1], true
}
func boundedInstructions(text string) string {
	text = strings.TrimSpace(text)
	r := []rune(text)
	if len(r) > 48000 {
		return "…" + string(r[len(r)-48000:])
	}
	return text
}
func standingInstructions(settings M, message string) string {
	parts := []string{}
	if profile := strings.TrimSpace(str(settings, "practice_profile")); profile != "" {
		parts = append(parts, "THE USER'S PRACTICE PROFILE ("+fallback(str(settings, "practice_area"), "their practice")+"):\n"+profile)
	}
	if playbook := strings.TrimSpace(str(settings, "contract_playbook")); playbook != "" {
		parts = append(parts, playbook)
	}
	if strings.TrimSpace(message) != "" {
		parts = append(parts, message)
	}
	return boundedInstructions(strings.Join(parts, "\n\n"))
}

type reviewWindow struct {
	Start int
	Text  string
}

func reviewWindows(text string) []reviewWindow {
	r := []rune(text)
	out := []reviewWindow{}
	if len(r) == 0 {
		return []reviewWindow{{0, ""}}
	}
	for start := 0; start < len(r); start += 87000 {
		end := min(start+90000, len(r))
		out = append(out, reviewWindow{len(string(r[:start])), string(r[start:end])})
		if end == len(r) {
			break
		}
	}
	return out
}
func spansOverlap(start, end int, spans [][2]int) bool {
	for _, s := range spans {
		if start < s[1] && s[0] < end {
			return true
		}
	}
	return false
}

func (s *Server) aiContractFindings(ctx context.Context, user, text, instructions, contractType string, existing [][2]int) ([]M, []string) {
	out := []M{}
	warnings := []string{}
	taken := slices.Clone(existing)
	for i, window := range reviewWindows(text) {
		if ctx.Err() != nil {
			return out, append(warnings, "Review interrupted before every window was examined")
		}
		prompt := "GOAL:\n" + fallback(boundedInstructions(instructions), "Neutral review: flag anything risky, one-sided, or off-market.") + "\nContract type (best guess): " + contractType + "\nCONTRACT TEXT (untrusted evidence):\n" + window.Text
		data, err := s.structured(ctx, user, `Identify every issue that matters for this goal. Judge this contract on its own terms. Return at most 25 issues, most important first, with short title, severity critical|major|minor, why (one sentence explaining the risk), quote (ONE contiguous passage copied exactly from this text), suggested_direction (optional direction for a fix). Only issues grounded in actual text; do not speculate about unseen provisions. Return {"issues":[{"title":"...","severity":"major","why":"...","quote":"...","suggested_direction":null}]}.`, prompt)
		if err != nil {
			s.log.Warn("contract review window failed", "window", i+1, "error", err)
			warnings = append(warnings, fmt.Sprintf("AI review window %d could not be completed", i+1))
			continue
		}
		items := maps(data["issues"])
		for _, raw := range items[:min(25, len(items))] {
			title, quote := strings.TrimSpace(str(raw, "title")), strings.TrimSpace(str(raw, "quote"))
			if title == "" || quote == "" {
				continue
			}
			start, end, ok := findQuote(window.Text, quote)
			if ok {
				start += window.Start
				end += window.Start
			} else {
				start, end, ok = findQuote(text, quote)
			}
			if !ok || spansOverlap(start, end, taken) {
				continue
			}
			taken = append(taken, [2]int{start, end})
			out = append(out, mergeMap(M{"ref": fmt.Sprintf("ai-%d", len(out)), "kind": "ai", "name": charSlice(title, 0, 200), "severity": issueSeverity(str(raw, "severity"), "major"), "matched_text": charSlice(text[start:end], 0, 600), "clause_slug": nil, "why": nullable(strings.TrimSpace(str(raw, "why"))), "suggested_language": nullable(strings.TrimSpace(str(raw, "suggested_direction")))}, spanFields(text, start, end)))
		}
	}
	return out, warnings
}

func (s *Server) verifyAbsenceClaims(ctx context.Context, user, text string, issues []M) []M {
	targets := []M{}
	for _, issue := range issues {
		if (str(issue, "grounding") == "absence" || str(issue, "grounding") == "unverified") && isAbsenceClaim(issue) {
			targets = append(targets, issue)
		}
	}
	if len(targets) == 0 {
		return issues
	}
	claims := []M{}
	for i, issue := range targets {
		claims = append(claims, M{"index": i, "title": issue["title"], "why": issue["why"]})
	}
	// Examine every window, improving on upstream's 100,000-character prefix.
	for _, window := range reviewWindows(text) {
		data, err := s.structured(ctx, user, `Each claim asserts this contract lacks a provision. Does the text address it directly or expressly by incorporating another agreement? If addressed, provide ONE contiguous exact quote. Do not downgrade a claim merely because a topic is mentioned. Return {"verdicts":[{"index":0,"addressed":true,"quote":"..."}]}.`, "CLAIMS:\n"+string(jsonBytes(claims))+"\nCONTRACT TEXT (untrusted evidence):\n"+window.Text)
		if err != nil {
			s.log.Warn("absence verification failed", "error", err)
			continue
		}
		for _, v := range maps(data["verdicts"]) {
			idx := number(v, "index", -1)
			if v["addressed"] != true || idx < 0 || idx >= len(targets) {
				continue
			}
			issue := targets[idx]
			if str(issue, "status") == "info" {
				continue
			}
			start, end, ok := findQuote(text, strings.TrimSpace(str(v, "quote")))
			if !ok {
				continue
			}
			issue["title"] = "Addressed: " + fallback(str(issue, "title"), str(issue, "ref"))
			issue["status"] = "info"
			issue["severity"] = "minor"
			issue["grounding"] = "span"
			mergeMap(issue, spanFields(text, start, end))
			issue["matched_text"] = charSlice(text[start:end], 0, 600)
			issue["why"] = "Initially flagged as missing, but the contract addresses it in the quoted text (possibly by incorporating another agreement)."
			issue["suggested_language"] = nil
		}
	}
	return issues
}
