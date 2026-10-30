package app

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const courtMethodNotice = "This is a scan of the language courts use near the citation in CourtListener search results — not an editorial citator. It does not replace Shepard's or KeyCite; read the flagged opinions before relying on the case."

var courtNegativeRE = regexp.MustCompile(`(?i)\b(overrul(?:e|ed|es|ing)|abrogat(?:e|ed|es|ing)|disapprov(?:e|ed|es|ing)|reject(?:ed|s|ing)? (?:the )?(?:reasoning|holding|rule|analysis|approach|rationale|conclusion) (?:of|in)|no longer good law|superseded by statute|reversed on|wrongly decided|erroneously decided|declin(?:e|ed|es|ing) to follow|receded from|repudiat(?:e|ed|es|ing)|rev['’]d(?: in part)?|vacated(?: in part)?)\b`)
var courtOtherGroundsRE = regexp.MustCompile(`(?i)\b(overrul|abrogat|disapprov|reject|supersed|rev['’]d|vacat)\w*\s+(?:in part\s+)?on other grounds\b`)
var courtCautionRE = regexp.MustCompile(`(?i)\b(distinguish(?:ed|able|es|ing)?|questioned|criticiz(?:ed|es|ing)|limited to (?:its|their) facts|narrow(?:ed|ing)|cast(?:s)? doubt|called into doubt|doubted|on other grounds)\b`)
var courtPositiveRE = regexp.MustCompile(`(?i)\b(followed|affirm(?:ed|s|ing)|approved|adopted|relied (?:up)?on|controlling|reaffirm(?:ed|s|ing)|endorsed|consistent with)\b`)
var courtCiteLikeRE = regexp.MustCompile("(?:" + str(caseGuardPatterns, "_REPORTER_CITE_RE") + "|" + str(caseGuardPatterns, "_WL_CITE_RE") + `|\d+\s+[A-Za-z.]+\s*\d*[a-z]*\s+\d+)`)
var courtNormalizeRE = regexp.MustCompile(`[\s.]`)
var courtWhitespaceRE = regexp.MustCompile(`[ \t]+`)

type courtContactKey struct{}

func courtText(text string) string {
	return strings.TrimSpace(courtWhitespaceRE.ReplaceAllString(html.UnescapeString(stripTags.ReplaceAllString(text, " ")), " "))
}
func courtID(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', 0, 64)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	}
	return ""
}
func courtURL(path string) any {
	if strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "//") {
		return "https://www.courtlistener.com" + path
	}
	if strings.HasPrefix(path, "https://www.courtlistener.com/") {
		return path
	}
	return nil
}
func courtTreatment(text string) string {
	text = courtOtherGroundsRE.ReplaceAllString(courtText(text), "on other grounds")
	if courtNegativeRE.MatchString(text) {
		return "negative"
	}
	if courtCautionRE.MatchString(text) {
		return "caution"
	}
	if courtPositiveRE.MatchString(text) {
		return "positive"
	}
	return "neutral"
}
func courtVerdict(negative, caution, read int) (string, any) {
	if negative >= 3 {
		return "danger", nil
	}
	if negative >= 1 || caution >= 3 {
		return "warning", nil
	}
	if caution > 0 {
		return "caution", nil
	}
	if read == 0 {
		return "unknown", nil
	}
	return "none", true
}
func courtMatches(query string, result M) bool {
	if courtCiteLikeRE.MatchString(query) {
		wanted := strings.ToLower(courtNormalizeRE.ReplaceAllString(courtCiteLikeRE.FindString(query), ""))
		for _, cite := range stringsOf(result["citation"]) {
			have := strings.ToLower(courtNormalizeRE.ReplaceAllString(cite, ""))
			if have != "" && have == wanted {
				return true
			}
		}
		return false
	}
	stop := strings.Fields("v vs the of and in re ex parte rel et al matter inc llc llp corp co ltd")
	have := partyWordsRE.FindAllString(strings.ToLower(str(result, "caseName")+" "+str(result, "caseNameShort")), -1)
	count := 0
	for _, word := range partyWordsRE.FindAllString(strings.ToLower(query), -1) {
		if len(word) > 1 && !slices.Contains(stop, word) {
			count++
			if !slices.Contains(have, word) {
				return false
			}
		}
	}
	return count > 0
}

func courtSearchOpinion(result M) M {
	ops := maps(result["opinions"])
	first := M{}
	if len(ops) > 0 {
		first = ops[0]
	}
	id := courtID(first["id"])
	if id == "" {
		id = courtID(result["id"])
	}
	if id == "" || id == "0" {
		return nil
	}
	return M{"id": id, "opinion_id": id, "cluster_id": nullable(courtID(result["cluster_id"])), "case_name": fallback(str(result, "caseName"), "Unknown"), "case_name_short": result["caseNameShort"], "citation": stringsOf(result["citation"]), "court": fallback(str(result, "court"), str(result, "court_id")), "court_id": result["court_id"], "date_filed": result["dateFiled"], "absolute_url": courtURL(str(result, "absolute_url")), "snippet": fallback(str(first, "snippet"), str(result, "snippet")), "score": object(object(result["meta"])["score"])["bm25"]}
}

func (s *Server) courtOpinionText(ctx context.Context, user, id string) (string, error) {
	if _, err := strconv.ParseUint(id, 10, 64); err != nil {
		return "", bad("Invalid opinion id")
	}
	data, err := s.courtJSON(ctx, user, "/opinions/"+id+"/", nil)
	if err != nil {
		return "", err
	}
	if text := str(data, "plain_text"); strings.TrimSpace(text) != "" {
		return text, nil
	}
	for _, key := range []string{"html_with_citations", "html", "html_lawbox", "html_columbia", "xml_harvard"} {
		if text := courtText(str(data, key)); text != "" {
			return text, nil
		}
	}
	return "", nil
}

func (s *Server) courtResearchPassages(ctx context.Context, user, query string) ([]M, error) {
	data, err := s.courtJSON(ctx, user, "/search/", url.Values{"q": {query}, "type": {"o"}, "order_by": {"score desc"}, "page_size": {"3"}, "cited_gt": {"2"}})
	if err != nil {
		return nil, err
	}
	out := []M{}
	for _, result := range maps(data["results"]) {
		if len(out) >= 3 {
			break
		}
		op := courtSearchOpinion(result)
		if op == nil {
			continue
		}
		text, e := s.courtOpinionText(ctx, user, str(op, "id"))
		source := "opinion"
		if e != nil || strings.TrimSpace(text) == "" {
			text = courtText(str(op, "snippet"))
			source = "snippet"
		} else {
			text = researchExcerpt(text, query, 4000)
		}
		if text == "" {
			continue
		}
		citation := ""
		if cites := stringsOf(op["citation"]); len(cites) > 0 {
			citation = cites[0]
		}
		out = append(out, M{"source_type": "case_law", "title": op["case_name"], "text": text, "excerpt_source": source, "opinion_id": op["id"], "cluster_id": op["cluster_id"], "citation": nullable(citation), "url": op["absolute_url"], "court": op["court"], "date_filed": op["date_filed"], "score": op["score"]})
	}
	return out, nil
}

func (s *Server) courtCitingOpinions(ctx context.Context, user string, target M, citation, order string) ([]M, error) {
	ids := []string{}
	for _, op := range maps(target["opinions"]) {
		if id := courtID(op["id"]); id != "" && id != "0" {
			if _, err := strconv.ParseUint(id, 10, 64); err == nil {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		id := courtID(target["cluster_id"])
		if _, err := strconv.ParseUint(id, 10, 64); err != nil {
			return nil, bad("The case's opinion identifiers could not be resolved")
		}
		cluster, err := s.courtJSON(ctx, user, "/clusters/"+id+"/", nil)
		if err != nil {
			return nil, err
		}
		for _, raw := range stringsOf(cluster["sub_opinions"]) {
			parts := strings.Split(strings.TrimRight(raw, "/"), "/")
			id := parts[len(parts)-1]
			if _, err := strconv.ParseUint(id, 10, 64); err == nil {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return nil, bad("The case's opinion identifiers could not be resolved")
	}
	expr := "cites:(" + strings.Join(uniqueWorkflowStrings(ids), " OR ") + ")"
	params := url.Values{"q": {expr}, "type": {"o"}, "page_size": {"100"}, "order_by": {order}}
	listing, err := s.courtJSON(ctx, user, "/search/", params)
	if err != nil {
		return nil, err
	}
	results := maps(listing["results"])
	contexts := map[string]string{}
	name := fallback(str(target, "caseNameShort"), str(target, "caseName"))
	terms := []string{}
	for _, term := range []string{citation, name} {
		if len(strings.TrimSpace(term)) > 3 {
			terms = append(terms, `"`+strings.ReplaceAll(strings.ReplaceAll(term, `\`, `\\`), `"`, `\"`)+`"`)
		}
	}
	if len(results) > 0 && len(terms) > 0 {
		params.Set("q", expr+" ("+strings.Join(terms, " OR ")+")")
		params.Set("highlight", "on")
		focused, e := s.courtJSON(ctx, user, "/search/", params)
		if e == nil {
			for _, r := range maps(focused["results"]) {
				ops := maps(r["opinions"])
				if len(ops) > 0 && strings.Contains(str(ops[0], "snippet"), "<mark>") {
					contexts[fallback(courtID(r["cluster_id"]), courtID(r["id"]))] = str(ops[0], "snippet")
				}
			}
		}
	}
	out := []M{}
	for _, r := range results {
		id := fallback(courtID(r["cluster_id"]), courtID(r["id"]))
		snippet := contexts[id]
		found := snippet != ""
		treatment := "neutral"
		if found {
			index := strings.Index(snippet, "<mark>")
			runes := []rune(snippet)
			at := len([]rune(snippet[:index]))
			treatment = courtTreatment(string(runes[max(0, at-260):min(len(runes), at+260)]))
		} else {
			if ops := maps(r["opinions"]); len(ops) > 0 {
				snippet = str(ops[0], "snippet")
			}
		}
		idNumber, _ := strconv.ParseInt(id, 10, 64)
		out = append(out, M{"id": idNumber, "case_name": fallback(legalStripMarks(str(r, "caseName")), "Unknown"), "citation": stringsOf(r["citation"]), "court": r["court"], "date_filed": legalDate(r["dateFiled"]), "treatment": treatment, "depth": 1, "snippet": nullable(charSlice(courtText(snippet), 0, 600)), "context_found": found, "url": courtURL(str(r, "absolute_url"))})
	}
	return out, nil
}

func (s *Server) validateCourtCitation(ctx context.Context, user, citation string) (M, error) {
	key, err := s.providerKey(ctx, user, "courtlistener", s.cfg.CourtListenerToken)
	if err != nil {
		return nil, err
	}
	if key == "" {
		return nil, bad("Add a CourtListener API token in Settings to check citation treatment")
	}
	query := citation
	if courtCiteLikeRE.MatchString(citation) {
		query = `citation:"` + courtCiteLikeRE.FindString(citation) + `"`
	}
	data, err := s.courtJSON(ctx, user, "/search/", url.Values{"q": {query}, "type": {"o"}, "page_size": {"5"}})
	if err != nil {
		return nil, err
	}
	var target M
	for _, r := range maps(data["results"]) {
		if courtMatches(citation, r) {
			target = r
			break
		}
	}
	result := M{"case_id": 0, "case_name": "Not Found", "citation": citation, "absolute_url": nil, "is_good_law": nil, "warning_level": "unknown", "method": "citing_language_scan", "negative_citations": []M{}, "caution_citations": []M{}, "positive_citations": []M{}, "neutral_count": 0, "total_citing_cases": 0, "citing_cases_analyzed": 0, "last_cited": nil, "notes": []string{courtMethodNotice}, "analysis_basis": "not_found"}
	if target == nil {
		note := "Citation not found in CourtListener database. Try a different format (e.g., '410 U.S. 113' or 'Roe v. Wade')."
		if rows := maps(data["results"]); len(rows) > 0 {
			if nearest := legalStripMarks(str(rows[0], "caseName")); nearest != "" {
				note = fmt.Sprintf("No case in CourtListener matched this citation exactly, so nothing was checked. The closest search result was %q — if that is the case you meant, search by its reporter citation (e.g., '410 U.S. 113').", nearest)
			}
		}
		result["notes"] = []string{note, courtMethodNotice}
		return result, nil
	}
	mergeMap(result, M{"case_id": number(target, "cluster_id", number(target, "id", 0)), "case_name": legalStripMarks(str(target, "caseName")), "absolute_url": courtURL(str(target, "absolute_url")), "total_citing_cases": number(target, "citeCount", 0)})
	citeTerm := courtCiteLikeRE.FindString(citation)
	if !courtCiteLikeRE.MatchString(citation) {
		citeTerm = ""
		if cites := stringsOf(target["citation"]); len(cites) > 0 {
			citeTerm = cites[0]
		}
	}
	recent, e1 := s.courtCitingOpinions(ctx, user, target, citeTerm, "dateFiled desc")
	most, e2 := s.courtCitingOpinions(ctx, user, target, citeTerm, "citeCount desc")
	// An unavailable listing must never be presented as a clean scan.
	partial := e1 != nil || e2 != nil
	if e1 != nil && e2 != nil {
		result["analysis_basis"] = "citing_lookup_failed"
		result["notes"] = []string{courtMethodNotice, "Could not verify: the opinions citing this case could not be looked up in CourtListener, so nothing was scanned. This is not a clean result — try again, or check the case in a citator."}
		return result, nil
	}
	byID := map[string]M{}
	order := []string{}
	for _, r := range append(most, recent...) {
		id := courtID(r["id"])
		old, exists := byID[id]
		if !exists {
			order = append(order, id)
		}
		if !exists || r["context_found"] == true && old["context_found"] != true {
			byID[id] = r
		}
	}
	groups := map[string][]M{"negative": {}, "caution": {}, "positive": {}, "neutral": {}}
	read := 0
	last := ""
	for _, id := range order {
		r := byID[id]
		groups[str(r, "treatment")] = append(groups[str(r, "treatment")], r)
		if r["context_found"] == true {
			read++
		}
		if date := str(r, "date_filed"); date > last {
			last = date
		}
	}
	warning, good := courtVerdict(len(groups["negative"]), len(groups["caution"]), read)
	if partial && good == true {
		warning, good = "unknown", nil
	}
	notes := courtTreatmentNotes(number(result, "total_citing_cases", 0), len(order), read, len(groups["negative"]), len(groups["caution"]), len(groups["positive"]))
	if partial {
		notes = append(notes, "One citing-opinion listing failed; the scan is incomplete.")
	}
	for _, kind := range []string{"negative", "caution", "positive"} {
		rows := groups[kind]
		result[kind+"_citations"] = rows[:min(25, len(rows))]
	}
	basis := "opening_text_only"
	if read > 0 {
		basis = "language_near_citation"
	}
	mergeMap(result, M{"warning_level": warning, "is_good_law": good, "neutral_count": len(groups["neutral"]), "citing_cases_analyzed": len(order), "last_cited": nullable(last), "notes": notes, "analysis_basis": basis})
	if number(result, "total_citing_cases", 0) == 0 {
		result["total_citing_cases"] = len(order)
	}
	return result, nil
}

func courtTreatmentNotes(total, retrieved, read, negative, caution, positive int) []string {
	notes := []string{courtMethodNotice}
	if total > 0 {
		count := strconv.Itoa(total)
		for i := len(count) - 3; i > 0; i -= 3 {
			count = count[:i] + "," + count[i:]
		}
		notes = append(notes, "Cited by "+count+" opinions in CourtListener.")
	}
	if retrieved > 0 {
		notes = append(notes, fmt.Sprintf("Scanned the passage around the citation in %d of the %d most recent and most-cited citing opinions retrieved; the rest could not be located in the text and were not classified.", read, retrieved))
	} else {
		notes = append(notes, "No citing opinions could be retrieved — the case may be too recent or the search was limited.")
	}
	if negative > 0 {
		notes = append(notes, fmt.Sprintf("%d citing opinion(s) use negative language near the citation (overruled, abrogated, declined to follow…). Read each before relying on the case.", negative))
	}
	if caution > 0 {
		notes = append(notes, fmt.Sprintf("%d citing opinion(s) use cautionary language (distinguished, questioned, limited, on other grounds…).", caution))
	}
	if positive > 0 {
		notes = append(notes, fmt.Sprintf("%d citing opinion(s) use positive language (followed, affirmed, relied on…).", positive))
	}
	if read == 0 && retrieved > 0 {
		notes = append(notes, "None of the retrieved opinions could be read at the citation, so no treatment signal is available — this is not a clean result.")
	}
	return notes
}

func (s *Server) checkResearchCitation(ctx context.Context, user, citation string) M {
	result, err := s.validateCourtCitation(ctx, user, citation)
	if err != nil {
		status := "error"
		key, _ := s.providerKey(ctx, user, "courtlistener", s.cfg.CourtListenerToken)
		if key == "" {
			status = "unavailable"
		}
		return M{"citation": citation, "status": status, "detail": workflowSafeError(err)}
	}
	return M{"citation": citation, "status": "checked", "case_name": result["case_name"], "warning_level": result["warning_level"], "is_good_law": result["is_good_law"], "url": result["absolute_url"], "negative": len(maps(result["negative_citations"])), "caution": len(maps(result["caution_citations"]))}
}

func findWorkflowCitations(text string) []string {
	out := []string{}
	for _, re := range []*regexp.Regexp{reporterCiteRE, westlawCiteRE} {
		for _, cite := range re.FindAllString(text, -1) {
			out = append(out, strings.Join(strings.Fields(cite), " "))
		}
	}
	return uniqueWorkflowStrings(out)
}
func (s *Server) workflowCitationCheck(ctx context.Context, user string, p, outputs M) (M, error) {
	citations := []string{}
	for _, c := range stringsOf(p["citations"]) {
		if c = strings.Join(strings.Fields(c), " "); c != "" {
			citations = append(citations, c)
		}
	}
	names := stringsOf(p["from_steps"])
	if p["from_steps"] == nil {
		for name := range outputs {
			names = append(names, name)
		}
		slices.Sort(names)
	}
	foundIn := M{}
	for _, name := range names {
		prior := workflowOutput(outputs, name)
		if prior == nil {
			return nil, bad(fmt.Sprintf("Step '%s' has no output to scan for citations", name))
		}
		found := findWorkflowCitations(workflowJSON(prior))
		if len(found) > 0 {
			foundIn[name] = found
			citations = append(citations, found...)
		}
	}
	citations = uniqueWorkflowStrings(citations)
	cap := min(len(citations), number(s.workflowCaps(), "max_citations", 10))
	skipped := append([]string{}, citations[cap:]...)
	citations = citations[:cap]
	if len(citations) == 0 {
		return workflowStepOutput("No citations to check.", M{"citations": []M{}, "found_in_steps": foundIn, "skipped": []string{}}, nil, nil), nil
	}
	results := []M{}
	negative, caution, unavailable := 0, 0, 0
	for _, cite := range citations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r := s.checkResearchCitation(ctx, user, cite)
		results = append(results, r)
		if str(r, "status") != "checked" {
			unavailable++
		} else if number(r, "negative", 0) > 0 {
			negative++
		} else if number(r, "caution", 0) > 0 {
			caution++
		}
	}
	summary := fmt.Sprintf("Checked %s: %d with negative treatment language, %d with cautionary language, %d could not be checked.", workflowPlural(len(results), "citation"), negative, caution, unavailable)
	return workflowStepOutput(summary, M{"citations": results, "found_in_steps": foundIn, "skipped": skipped}, nil, nil), nil
}
