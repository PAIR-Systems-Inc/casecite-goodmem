package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Follow provider pagination only on the configured origin and resource. Never
// forward a personal API token to a link received inside a provider response.
func (s *Server) judgeCursor(raw, path, kind string) (url.Values, error) {
	base, _ := url.Parse(strings.TrimRight(fallback(s.cfg.CourtListenerURL, "https://www.courtlistener.com/api/rest/v4"), "/"))
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != base.Scheme || u.User != nil || u.Fragment != "" || u.RawPath != "" || u.Opaque != "" || u.Path != base.Path+path {
		return nil, fail(502, "CourtListener returned invalid pagination")
	}
	hostMatch := strings.EqualFold(u.Host, base.Host)
	if base.Host == "www.courtlistener.com" || base.Host == "courtlistener.com" {
		hostMatch = u.Host == "www.courtlistener.com" || u.Host == "courtlistener.com"
	}
	if !hostMatch {
		return nil, fail(502, "CourtListener returned invalid pagination")
	}
	params, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, fail(502, "CourtListener returned invalid pagination")
	}
	if kind != "" {
		if k := params.Get("type"); k != "" && k != kind {
			return nil, fail(502, "CourtListener changed the search type")
		}
		params.Set("type", kind)
	}
	params.Set("page_size", "100")
	return params, nil
}
func (s *Server) judgePages(ctx context.Context, user, path string, params url.Values, limit int, idKey string) ([]M, int, error) {
	rows := []M{}
	ids, cursors := map[string]bool{}, map[string]bool{}
	total := 0
	for page := 0; page < 20 && len(rows) < limit; page++ {
		data, err := s.courtJSON(ctx, user, path, params)
		if err != nil {
			return rows, total, err
		}
		if page == 0 {
			total = int(judgeNumber(data, "count"))
		}
		batch := maps(data["results"])
		if len(batch) == 0 {
			break
		}
		for _, r := range batch {
			id := fallback(courtID(r[idKey]), courtID(r["id"]))
			if id == "" || ids[id] {
				continue
			}
			ids[id] = true
			rows = append(rows, r)
			if len(rows) >= limit {
				break
			}
		}
		next := str(data, "next")
		if next == "" || len(rows) >= limit {
			break
		}
		if cursors[next] {
			return rows, total, fail(502, "CourtListener repeated a pagination cursor")
		}
		cursors[next] = true
		params, err = s.judgeCursor(next, path, params.Get("type"))
		if err != nil {
			return rows, total, err
		}
	}
	return rows, total, nil
}
func (s *Server) judgeSearch(ctx context.Context, user, query string, limit int) (M, error) {
	query = strings.TrimSpace(query)
	parts := strings.Fields(query)
	params := url.Values{"page_size": {"100"}}
	if len(parts) > 1 {
		params.Set("name_first__icontains", parts[0])
		params.Set("name_last__icontains", parts[len(parts)-1])
	} else {
		params.Set("name_last__icontains", query)
	}
	rows, total, err := s.judgePages(ctx, user, "/people/", params, limit, "id")
	if err != nil {
		var upstream *courtHTTPError
		// The search index can remain available when the people endpoint fails.
		// Rejected credentials, throttling and invalid cursors never fall back.
		if !errors.As(err, &upstream) || (upstream.status != 400 && upstream.status != 404 && upstream.status != 405 && upstream.status < 500) {
			return nil, err
		}
		rows, total, err = s.judgePages(ctx, user, "/search/", url.Values{"q": {"name:" + query}, "type": {"p"}, "page_size": {"100"}}, limit, "id")
		if err != nil {
			return nil, err
		}
	}
	judges := []M{}
	ids := map[string]bool{}
	for _, r := range rows {
		judges = append(judges, judgeSummary(r))
		ids[courtID(r["id"])] = true
	}
	if len(parts) == 1 && len(judges) < limit {
		data, e := s.courtJSON(ctx, user, "/people/", url.Values{"name_first__icontains": {query}, "page_size": {"50"}})
		if e == nil {
			for _, r := range maps(data["results"]) {
				id := courtID(r["id"])
				if id == "" || ids[id] {
					continue
				}
				ids[id] = true
				judges = append(judges, judgeSummary(r))
				total++
				if len(judges) >= limit {
					break
				}
			}
		}
	}
	judgeSortMatches(judges, query)
	return M{"judges": judges, "count": len(judges), "total_available": max(total, len(judges))}, nil
}
func (s *Server) judgeLinked(ctx context.Context, user string, ref any, resource string) (M, error) {
	if m := object(ref); len(m) > 0 {
		return m, nil
	}
	id := legalResourceID(legalScalar(ref), resource)
	if id == "" {
		return nil, fail(502, "CourtListener returned an invalid "+resource+" reference")
	}
	return s.courtJSON(ctx, user, "/"+resource+"/"+id+"/", nil)
}
func (s *Server) judgeEducation(ctx context.Context, user string, refs any) ([]M, int) {
	rows := []M{}
	missing := 0
	for _, ref := range judgeRefs(refs) {
		edu, err := s.judgeLinked(ctx, user, ref, "educations")
		if err != nil {
			missing++
			continue
		}
		school := object(edu["school"])
		if len(school) == 0 && strings.HasPrefix(str(edu, "school"), "http") {
			school, err = s.judgeLinked(ctx, user, edu["school"], "schools")
			if err != nil {
				missing++
			}
		}
		var schoolName any = school["name"]
		if len(school) == 0 && !strings.HasPrefix(str(edu, "school"), "http") {
			schoolName = nullable(str(edu, "school"))
		}
		rows = append(rows, M{"school_name": schoolName, "school_type": school["type"], "degree": nullable(fallback(str(edu, "degree_level"), str(edu, "degree_detail"))), "degree_year": edu["degree_year"]})
	}
	return rows, missing
}
func judgeRefs(value any) []any {
	switch v := value.(type) {
	case []any:
		return v
	case []M:
		out := []any{}
		for _, m := range v {
			out = append(out, m)
		}
		return out
	case []string:
		out := []any{}
		for _, s := range v {
			out = append(out, s)
		}
		return out
	}
	return nil
}
func (s *Server) judgePositions(ctx context.Context, user string, refs any) ([]M, int) {
	rows := []M{}
	missing := 0
	for _, ref := range judgeRefs(refs) {
		pos, err := s.judgeLinked(ctx, user, ref, "positions")
		if err != nil {
			missing++
			continue
		}
		court := object(pos["court"])
		var courtName any
		if len(court) == 0 && strings.HasPrefix(str(pos, "court"), "http") {
			court, err = s.judgeLinked(ctx, user, pos["court"], "courts")
			if err != nil {
				missing++
			}
		}
		if len(court) > 0 {
			courtName = nullable(fallback(str(court, "full_name"), str(court, "short_name")))
		} else if !strings.HasPrefix(str(pos, "court"), "http") {
			courtName = nullable(str(pos, "court"))
		}
		appointer := pos["appointer"]
		if strings.HasPrefix(str(pos, "appointer"), "http") {
			person, e := s.judgeLinked(ctx, user, appointer, "people")
			appointer = nil
			if e == nil {
				appointer = judgeName(person)
			} else {
				missing++
			}
		}
		rows = append(rows, M{"position_type": judgeString(pos["position_type"]), "court_name": courtName, "court_id": court["id"], "appointer": judgeString(appointer), "date_start": judgeString(pos["date_start"]), "date_termination": judgeString(pos["date_termination"]), "termination_reason": judgeString(pos["termination_reason"])})
	}
	return rows, missing
}
func judgeAuthorID(op M) string {
	return fallback(courtID(op["author_id"]), legalResourceID(legalScalar(op["author"]), "people"))
}
func judgeOpinionMetadata(r M, judgeID string) M {
	id := r["cluster_id"]
	if courtID(id) == "" {
		id = r["id"]
	}
	citations := legalOrderCitations(r["citation"])
	cite := ""
	if len(citations) > 0 {
		cite = citations[0]
	}
	refs, authoredTypes, authoredIDs := []string{}, []string{}, []string{}
	attribution := "name_match"
	for _, v := range judgeRefs(r["panel_ids"]) {
		if courtID(v) == judgeID {
			attribution = "panel"
		}
	}
	for _, part := range maps(r["opinions"]) {
		if opID := courtID(part["id"]); opID != "" {
			refs = append(refs, opID)
			if judgeAuthorID(part) == judgeID {
				authoredIDs = append(authoredIDs, opID)
				authoredTypes = append(authoredTypes, str(part, "type"))
				attribution = "author"
			}
		}
	}
	// Search v3-style rows can still carry the individual opinion on the root.
	if len(refs) == 0 && courtID(r["cluster_id"]) != "" && courtID(r["id"]) != "" {
		refs = append(refs, courtID(r["id"]))
		if judgeAuthorID(r) == judgeID {
			authoredIDs = append(authoredIDs, courtID(r["id"]))
			authoredTypes = append(authoredTypes, fallback(str(r, "type"), str(r, "opinion_type")))
			attribution = "author"
		}
	}
	var opinionType any
	if len(authoredTypes) > 0 {
		opinionType = authoredTypes[0]
	}
	perCuriam := 0
	if r["per_curiam"] == true {
		perCuriam = 1
	}
	return M{"id": id, "case_name": fallback(legalStripMarks(str(r, "caseName")), "Unknown"), "case_name_short": r["caseNameShort"], "court": legalDefault(r, "court", ""), "court_id": legalDefault(r, "court_id", ""), "date_filed": r["dateFiled"], "date_argued": r["dateArgued"], "docket_number": r["docketNumber"], "citation": cite, "citation_count": legalDefault(r, "citeCount", 0), "precedential_status": r["status"], "opinion_type": opinionType, "per_curiam": perCuriam, "url": legalPublicURL(str(r, "absolute_url")), "snippet": charSlice(legalNestedSnippet(r), 0, 500), "full_text": "", "word_count": 0, "authored_text": "", "authored_word_count": 0, "opinion_ids": refs, "authored_opinion_ids": authoredIDs, "authored_opinion_types": authoredTypes, "attribution": attribution}
}

func (s *Server) judgeClusterText(ctx context.Context, user, clusterID, judgeID string) (M, error) {
	cluster, err := s.courtJSON(ctx, user, "/clusters/"+clusterID+"/", nil)
	if err != nil {
		return nil, err
	}
	refs := judgeRefs(cluster["sub_opinions"])
	parts, authoredText := []string{}, []string{}
	meta := []M{}
	ids, authoredIDs, types := []string{}, []string{}, []string{}
	failures := 0
	for _, ref := range refs[:min(6, len(refs))] {
		op, e := s.judgeLinked(ctx, user, ref, "opinions")
		if e != nil {
			failures++
			continue
		}
		if id := courtID(op["id"]); id != "" {
			ids = append(ids, id)
			if judgeAuthorID(op) == judgeID {
				authoredIDs = append(authoredIDs, id)
				types = append(types, str(op, "type"))
			}
		}
		text := str(op, "plain_text")
		if text == "" {
			for _, field := range []string{"html_with_citations", "html", "html_lawbox", "html_columbia", "xml_harvard"} {
				if raw := str(op, field); raw != "" {
					text = courtText(raw)
					break
				}
			}
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		label := fallback(legalToolAssets.OpinionTypes[str(op, "type")], "Opinion")
		author := strings.TrimSpace(str(op, "author_str"))
		meta = append(meta, M{"id": op["id"], "type": op["type"], "type_label": label, "author": nullable(author), "words": len(strings.Fields(text))})
		if judgeID != "" && judgeAuthorID(op) == judgeID {
			authoredText = append(authoredText, text)
		}
		if len(refs) > 1 {
			header := label
			if author != "" {
				header += " — " + author
			}
			text = header + "\n\n" + text
		}
		parts = append(parts, text)
	}
	text := charSlice(strings.Join(parts, "\n\n\n"), 0, 100000)
	ownText := charSlice(strings.Join(authoredText, "\n\n\n"), 0, 100000)
	return M{"full_text": text, "word_count": len(strings.Fields(text)), "opinion_parts": meta, "opinion_ids": ids, "authored_text": ownText, "authored_word_count": len(strings.Fields(ownText)), "authored_opinion_ids": authoredIDs, "authored_opinion_types": types, "text_unavailable": text == "", "text_partial": failures > 0 || len(refs) > 6}, nil
}
func judgeDocketMetadata(r M, id string) M {
	judgeID, _ := strconv.ParseInt(id, 10, 64)
	docketID := r["docket_id"]
	if courtID(docketID) == "" {
		docketID = r["id"]
	}
	return M{"id": docketID, "judge_id": judgeID, "case_name": fallback(legalStripMarks(str(r, "caseName")), "Unknown"), "case_name_short": r["caseNameShort"], "court": legalDefault(r, "court", ""), "court_id": legalDefault(r, "court_id", ""), "date_filed": r["dateFiled"], "date_terminated": r["dateTerminated"], "docket_number": r["docketNumber"], "nature_of_suit": r["suitNature"], "cause": r["cause"], "jury_demand": r["juryDemand"], "assigned_to": r["assignedTo"], "referred_to": r["referredTo"], "url": legalPublicURL(fallback(str(r, "docket_absolute_url"), str(r, "absolute_url")))}
}
func (s *Server) judgeBuildData(ctx context.Context, user, id string) (M, M, error) {
	lease, _ := ctx.Value(jobLeaseKey{}).(jobLease)
	progress := func(message string, fraction float64) { s.jobProgress(ctx, lease.ID, message, fraction) }
	progress("Fetching judge biographical data…", 0.05)
	person, err := s.courtJSON(ctx, user, "/people/"+id+"/", nil)
	if err != nil {
		return nil, nil, err
	}
	warnings := []string{}
	missing := 0
	progress("Processing education and career history…", 0.1)
	education, n := s.judgeEducation(ctx, user, person["educations"])
	missing += n
	positions, n := s.judgePositions(ctx, user, person["positions"])
	missing += n
	birthYear, _ := strconv.Atoi(charSlice(str(person, "date_dob"), 0, 4))
	progress("Checking Wikipedia biography identity…", 0.15)
	data := judgeBiography(id, person, s.judgeWikipedia(ctx, judgeName(person), birthYear), time.Now())
	data["education"], data["positions"] = education, positions
	if missing > 0 {
		warnings = append(warnings, fmt.Sprintf("%d linked biographical records could not be retrieved.", missing))
	}
	name := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(judgeName(person))
	progress("Fetching up to 300 linked cases…", 0.2)
	rows, total, err := s.judgePages(ctx, user, "/search/", url.Values{"q": {"author_id:" + id + " OR panel_ids:" + id + ` OR judge:"` + name + `"`}, "type": {"o"}, "page_size": {"100"}, "order_by": {"dateFiled desc"}}, 300, "cluster_id")
	if err != nil {
		return nil, nil, err
	} // A failed primary dataset must not become an empty profile.
	ops := []M{}
	for _, r := range rows {
		ops = append(ops, judgeOpinionMetadata(r, id))
	}
	data["coverage"] = M{"opinions_available": total, "opinions_retrieved": len(ops), "opinion_limit": 300, "full_text_limit": 25, "note": "Cases include identified authorship, panel participation and full-name matches. Separate-writing and writing metrics use explicitly identified authors only."}
	top := judgeSorted(ops, "citation_count", true)
	top = top[:min(25, len(top))]
	for i, op := range top {
		if err = ctx.Err(); err != nil {
			return nil, nil, err
		}
		progress(fmt.Sprintf("Reading full opinions: %d/%d…", i+1, len(top)), 0.25+0.4*float64(i)/float64(max(1, len(top))))
		text, e := s.judgeClusterText(ctx, user, courtID(op["id"]), id)
		if e != nil {
			warnings = append(warnings, "Full text unavailable for case "+courtID(op["id"])+".")
			continue
		}
		for k, v := range text {
			op[k] = v
		}
		if text["text_partial"] == true {
			warnings = append(warnings, "Some opinion parts are unavailable for case "+courtID(op["id"])+".")
		}
	}
	progress("Fetching up to 100 assigned dockets…", 0.7)
	rows, total, err = s.judgePages(ctx, user, "/search/", url.Values{"q": {"assigned_to_id:" + id + ` OR assignedTo:"` + name + `"`}, "type": {"d"}, "page_size": {"100"}, "order_by": {"dateFiled desc"}}, 100, "docket_id")
	dockets := []M{}
	if err != nil {
		warnings = append(warnings, "Assigned dockets could not be retrieved; case-duration and case-mix data are incomplete.")
	} else {
		for _, r := range rows {
			dockets = append(dockets, judgeDocketMetadata(r, id))
		}
	}
	coverage := object(data["coverage"])
	coverage["dockets_available"], coverage["dockets_retrieved"], coverage["docket_limit"] = total, len(dockets), 100
	progress("Analyzing citation relationships…", 0.85)
	ids := []string{}
	for _, op := range ops {
		for _, opID := range stringsOf(op["authored_opinion_ids"]) {
			if !slices.Contains(ids, opID) {
				ids = append(ids, opID)
			}
		}
	}
	citations := []M{}
	seen := map[string]bool{}
	judgeID, _ := strconv.ParseInt(id, 10, 64)
	for _, opID := range ids[:min(50, len(ids))] {
		if len(citations) >= 100 {
			break
		}
		if err = ctx.Err(); err != nil {
			return nil, nil, err
		}
		response, e := s.courtJSON(ctx, user, "/opinions-cited/", url.Values{"citing_opinion": {opID}, "page_size": {"20"}})
		if e != nil {
			warnings = append(warnings, "Some citation relationships could not be retrieved.")
			continue
		}
		for _, c := range maps(response["results"]) {
			cited := legalResourceID(legalScalar(c["cited_opinion"]), "opinions")
			if cited == "" || seen[opID+":"+cited] {
				continue
			}
			seen[opID+":"+cited] = true
			citingID, _ := strconv.ParseInt(opID, 10, 64)
			citations = append(citations, M{"judge_id": judgeID, "citing_opinion_id": citingID, "cited_opinion_id": cited, "depth": legalDefault(c, "depth", 1)})
			if len(citations) >= 100 {
				break
			}
		}
	}
	data["data_warnings"] = uniqueWorkflowStrings(warnings)
	if err = ctx.Err(); err != nil {
		return nil, nil, err
	}
	progress("Saving the judge profile…", 0.98)
	return data, M{"opinions": ops, "dockets": dockets, "citations": citations}, nil
}
