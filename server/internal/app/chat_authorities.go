package app

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type chatOpinionText struct {
	Ready      chan struct{}
	Text       string
	Characters int
	Err        error
}
type chatCourtReader struct {
	s     *Server
	user  string
	slots chan struct{}
	mu    sync.Mutex
	texts map[string]*chatOpinionText
}

func newChatCourtReader(s *Server, user string) *chatCourtReader {
	return &chatCourtReader{s: s, user: user, slots: make(chan struct{}, 6), texts: map[string]*chatOpinionText{}}
}

func (r *chatCourtReader) read(ctx context.Context, id string) *chatOpinionText {
	r.mu.Lock()
	if cached := r.texts[id]; cached != nil {
		r.mu.Unlock()
		select {
		case <-cached.Ready:
			return cached
		case <-ctx.Done():
			return &chatOpinionText{Err: ctx.Err()}
		}
	}
	entry := &chatOpinionText{Ready: make(chan struct{})}
	r.texts[id] = entry
	r.mu.Unlock()
	defer close(entry.Ready)
	text, err := r.s.courtOpinionText(ctx, r.user, id)
	entry.Characters, entry.Err = utf8.RuneCountInString(text), err
	// Do not retain arbitrarily large opinion bodies across a multi-point
	// brief. Only the bounded windows which can actually be judged are kept.
	entry.Text = charSlice(text, 0, 80000+(r.maxWindows()-1)*78000)
	return entry
}
func (r *chatCourtReader) maxWindows() int {
	n := r.s.cfg.ResearchOpinionWindows
	if n <= 0 {
		n = 6
	}
	return min(n, 32)
}

type chatOpinionVerdict struct {
	Reason, Quote             string
	Data                      M
	Partial                   bool
	WindowsRead, WindowsTotal int
}

func chatOpinionWindowCount(characters int) int { return 1 + (max(0, characters-80000)+77999)/78000 }

func (r *chatCourtReader) judge(ctx context.Context, opinion M, instructions, question, proposition, endorse string) chatOpinionVerdict {
	select {
	case r.slots <- struct{}{}:
	case <-ctx.Done():
		return chatOpinionVerdict{Reason: "error"}
	}
	defer func() { <-r.slots }()
	loaded := r.read(ctx, str(opinion, "id"))
	if loaded.Err != nil || strings.TrimSpace(loaded.Text) == "" {
		return chatOpinionVerdict{Reason: "unreadable"}
	}
	text := []rune(loaded.Text)
	total := chatOpinionWindowCount(loaded.Characters)
	verdict := chatOpinionVerdict{Reason: "unsupportive", WindowsTotal: total}
	for i := 0; i < min(total, r.maxWindows()); i++ {
		start := i * 78000
		window := string(text[start:min(len(text), start+80000)])
		data, err := r.s.structured(ctx, r.user, instructions,
			"Question: "+charSlice(question, 0, 1500)+"\nProposition: "+charSlice(proposition, 0, 1000)+"\nUntrusted opinion JSON:\n"+string(jsonBytes(M{"case_name": opinion["case_name"], "citation": opinion["citation"], "part": i + 1, "total_parts": total, "text": window})))
		if err != nil {
			verdict.Reason = "error"
			verdict.Partial = verdict.WindowsRead < total
			return verdict
		}
		verdict.WindowsRead++
		endorsed, valid := researchBool(data[endorse])
		if !valid || !endorsed {
			continue
		}
		quote := strings.TrimSpace(str(data, "quote"))
		lo, hi, found := findQuote(window, quote)
		if !found || chatContentRatio(quote) < .45 || utf8.RuneCountInString(quote) < 12 {
			verdict.Reason = "quote_unverified"
			continue
		}
		// Verify the entire proposed quotation. Salvaging one sentence from a
		// partly invented quote would validate a different proposition.
		verdict.Reason, verdict.Quote, verdict.Data = "attached", window[lo:hi], data
		verdict.Partial = verdict.WindowsRead < total
		return verdict
	}
	verdict.Partial = verdict.WindowsRead < total
	return verdict
}

func chatAuthorityStatus(requested bool) M {
	return M{"requested": requested, "points_searched": 0, "concepts": 0, "opinions_read": 0, "partially_read": 0, "attached": 0, "unreadable": 0, "unsupportive": 0, "quote_unverified": 0, "errors": 0, "searches_failed": 0, "windows_read": 0, "windows_total": 0}
}
func countChatOpinion(status M, verdict chatOpinionVerdict) {
	if verdict.Reason != "unreadable" {
		status["opinions_read"] = number(status, "opinions_read", 0) + 1
	}
	if verdict.Partial {
		status["partially_read"] = number(status, "partially_read", 0) + 1
	}
	status["windows_read"] = number(status, "windows_read", 0) + verdict.WindowsRead
	status["windows_total"] = number(status, "windows_total", 0) + verdict.WindowsTotal
	if verdict.Reason != "attached" {
		key := verdict.Reason
		if key == "error" {
			key = "errors"
		}
		status[key] = number(status, key, 0) + 1
	}
}

func (s *Server) chatCourtCandidates(ctx context.Context, user, query string, limit int, filters chatSearchQuery) ([]M, error) {
	params := url.Values{"q": {query}, "type": {"o"}, "order_by": {"score desc"}, "page_size": {strconv.Itoa(limit)}, "highlight": {"on"}}
	courts := legalResolveCourts(append(append([]string{}, filters.Courts...), filters.Jurisdictions...))
	if len(courts) > 0 {
		params.Set("court", strings.Join(courts, ","))
	}
	if len(courts) == 0 && len(filters.Courts)+len(filters.Jurisdictions) > 0 {
		return nil, bad("The court or jurisdiction filter was not recognized. Use a CourtListener court identifier.")
	}
	if filters.DateFrom != nil {
		params.Set("filed_after", *filters.DateFrom)
	}
	if filters.DateTo != nil {
		params.Set("filed_before", *filters.DateTo)
	}
	if len(courts) == 0 && filters.DateFrom == nil && filters.DateTo == nil {
		params.Set("cited_gt", "2")
	}
	data, err := s.courtJSON(ctx, user, "/search/", params)
	if err != nil {
		return nil, err
	}
	out := []M{}
	seen := map[string]bool{}
	for _, result := range maps(data["results"]) {
		// Search result IDs identify clusters. Never fetch a same-numbered
		// opinion unless the result actually supplies that opinion's ID.
		id := courtID(legalFirstOpinion(result))
		if id == "" || id == "0" || seen[id] {
			continue
		}
		op := courtSearchOpinion(result)
		if op == nil {
			continue
		}
		op["id"], op["opinion_id"] = id, id
		op["cluster_id"] = nullable(fallback(courtID(result["cluster_id"]), courtID(result["id"])))
		op["case_name"] = legalStripMarks(str(op, "case_name"))
		seen[id] = true
		out = append(out, op)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func chatAuthorityResult(opinion M, verdict chatOpinionVerdict, rank int) M {
	citations := stringsOf(opinion["citation"])
	citation := "No citation"
	if len(citations) > 0 {
		citation = citations[0]
	}
	why := fallback(str(verdict.Data, "why"), str(verdict.Data, "how"))
	why, _ = redactCaseReferences(why, []string{verdict.Quote}, []M{{"metadata": M{"case_name": opinion["case_name"], "citation": citation}}}, false)
	out := M{"id": "courtlistener_" + str(opinion, "id"), "text": charSlice(verdict.Quote, 0, 1500), "rank": rank, "similarity": 0,
		"metadata": M{"filename": str(opinion, "case_name") + " (" + citation + ")", "source": "courtlistener", "doc_type": "case_law", "court": opinion["court"],
			"date_filed": opinion["date_filed"], "citation": citation, "url": opinion["absolute_url"], "opinion_id": opinion["id"], "cluster_id": opinion["cluster_id"],
			"chunk_index": 0, "case_name": opinion["case_name"], "case_summary": nullable(why), "quote_verified": true, "partially_read": verdict.Partial, "windows_read": verdict.WindowsRead, "windows_total": verdict.WindowsTotal}}
	if score, ok := chatAuthorityRelevance(verdict.Data["relevance"]); ok {
		out["similarity"] = score
	}
	return out
}
func chatAuthorityRelevance(raw any) (float64, bool) {
	var score float64
	switch value := raw.(type) {
	case float64:
		score = value
	case int:
		score = float64(value)
	case string:
		var err error
		score, err = strconv.ParseFloat(value, 64)
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return score, !math.IsNaN(score) && !math.IsInf(score, 0) && score >= 0 && score <= 1
}

const chatAuthorityInstructions = `You are selecting legal authority for a lawyer's research request. Decide whether the provided part of an opinion states a rule of law, holding or reasoning that bears on the proposition. Off-topic keyword matches do not count. Return STRICT JSON: {"relevant":true|false,"quote":"ONE contiguous passage of 1–3 sentences copied EXACTLY, character for character, from the opinion text, stating the relevant rule or holding; empty when irrelevant","why":"one sentence on why this case matters for the research request","relevance":0.0}. The relevance is your 0–1 assessment of how directly the passage bears on the proposition. Never invent a quotation.`

func (s *Server) researchChatAuthorities(ctx context.Context, user, question, searchQuery string, limit int) ([]M, M, error) {
	phase, cancel := context.WithTimeout(ctx, 140*time.Second)
	defer cancel()
	status := chatAuthorityStatus(true)
	targets := []M{{"proposition": charSlice(question, 0, 400), "query": charSlice(fallback(searchQuery, question), 0, 80)}}
	data, err := s.structured(phase, user,
		`A lawyer is researching case law. Break the request into 1–3 distinct legal propositions. For each, give one sentence stating the rule of law or doctrine, without party names, and a 3–7 keyword search query. Return STRICT JSON: {"targets":[{"proposition":"...","query":"..."}]}.`,
		"Request: "+charSlice(question, 0, 1500))
	if err == nil {
		cleaned := []M{}
		for _, target := range maps(data["targets"]) {
			proposition, query := strings.TrimSpace(str(target, "proposition")), strings.TrimSpace(str(target, "query"))
			if proposition != "" && query != "" {
				cleaned = append(cleaned, M{"proposition": charSlice(proposition, 0, 1000), "query": charSlice(query, 0, 400)})
			}
			if len(cleaned) == 3 {
				break
			}
		}
		if len(cleaned) > 0 {
			targets = cleaned
		}
	}
	status["concepts"] = len(targets)
	type candidate struct {
		Opinion      M
		Propositions []string
		Verdict      chatOpinionVerdict
	}
	candidates := []candidate{}
	seen := map[string]int{}
	filters := parseChatSearch(question)
	for _, target := range targets {
		opinions, err := s.chatCourtCandidates(phase, user, str(target, "query"), 4, filters)
		if err != nil {
			status["searches_failed"] = number(status, "searches_failed", 0) + 1
			status["search_error"] = "CourtListener search could not complete. Check its configuration and retry."
			continue
		}
		for _, opinion := range opinions {
			if i, ok := seen[str(opinion, "id")]; ok {
				candidates[i].Propositions = append(candidates[i].Propositions, str(target, "proposition"))
				continue
			}
			seen[str(opinion, "id")] = len(candidates)
			candidates = append(candidates, candidate{Opinion: opinion, Propositions: []string{str(target, "proposition")}})
		}
	}
	reader := newChatCourtReader(s, user)
	var work sync.WaitGroup
	for i := range candidates {
		work.Add(1)
		go func(i int) {
			defer work.Done()
			c := &candidates[i]
			c.Verdict = reader.judge(phase, c.Opinion, chatAuthorityInstructions, question, strings.Join(uniqueWorkflowStrings(c.Propositions), "; "), "relevant")
		}(i)
	}
	work.Wait()
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	results := []M{}
	for _, c := range candidates {
		countChatOpinion(status, c.Verdict)
		if c.Verdict.Reason == "attached" && len(results) < limit {
			results = append(results, chatAuthorityResult(c.Opinion, c.Verdict, len(results)+1))
		}
	}
	status["attached"] = len(results)
	if phase.Err() != nil {
		status["timed_out"] = true
	}
	return results, status, nil
}

func chatAuthorityNotice(status M) string {
	if status["timed_out"] == true {
		return "Case-law research reached its time limit; only authorities verified before the deadline are included."
	}
	if number(status, "searches_failed", 0) > 0 {
		return fallback(str(status, "search_error"), "Some case-law searches could not complete.")
	}
	if number(status, "attached", 0) == 0 {
		return fmt.Sprintf("No verified supporting authorities were found (%d unreadable, %d not supportive, %d quotations unverified, %d review errors).", number(status, "unreadable", 0), number(status, "unsupportive", 0), number(status, "quote_unverified", 0), number(status, "errors", 0))
	}
	if number(status, "partially_read", 0) > 0 {
		return fmt.Sprintf("%d candidate opinion(s) were reviewed only in part; the attached quotations were verified against the text read.", number(status, "partially_read", 0))
	}
	return ""
}
