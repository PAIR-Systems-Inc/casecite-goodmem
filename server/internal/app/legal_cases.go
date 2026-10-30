package app

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

func (s *Server) legalCursor(cursor string) (url.Values, error) {
	base, _ := url.Parse(strings.TrimRight(fallback(s.cfg.CourtListenerURL, "https://www.courtlistener.com/api/rest/v4"), "/"))
	u, err := url.Parse(cursor)
	invalid := func() (url.Values, error) { return nil, bad("Invalid pagination cursor.") }
	if err != nil || base == nil || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.Scheme != base.Scheme || u.Path != strings.TrimRight(base.Path, "/")+"/search/" || u.RawPath != "" {
		return invalid()
	}
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		if u.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	hostsMatch := strings.EqualFold(base.Hostname(), u.Hostname())
	if base.Hostname() == "www.courtlistener.com" || base.Hostname() == "courtlistener.com" {
		hostsMatch = u.Hostname() == "www.courtlistener.com" || u.Hostname() == "courtlistener.com"
	}
	if !hostsMatch || port(base) != port(u) {
		return invalid()
	}
	params, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return invalid()
	}
	if kind := params.Get("type"); kind != "" && kind != "o" {
		return invalid()
	}
	params.Set("type", "o")
	if raw := params.Get("page_size"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			return invalid()
		}
	}
	return params, nil
}

func (s *Server) legalSearchCases(ctx context.Context, user, query, cursor string, pageSize int) (M, error) {
	params := url.Values{"q": {query}, "type": {"o"}, "page_size": {strconv.Itoa(pageSize)}, "highlight": {"on"}}
	if cursor != "" {
		var err error
		params, err = s.legalCursor(cursor)
		if err != nil {
			return nil, err
		}
	}
	data, err := s.courtJSON(ctx, user, "/search/", params)
	if err != nil {
		return nil, err
	}
	rows := []M{}
	for _, r := range maps(data["results"]) {
		id := r["cluster_id"]
		if legalScalar(id) == "" {
			id = r["id"]
		}
		rows = append(rows, M{"id": id, "opinion_id": legalFirstOpinion(r), "case_name": fallback(legalStripMarks(str(r, "caseName")), "Unknown"),
			"case_name_short": nullable(legalStripMarks(str(r, "caseNameShort"))), "case_name_full": nullable(legalStripMarks(str(r, "caseNameFull"))),
			"citation": legalOrderCitations(r["citation"]), "court": legalDefault(r, "court", ""), "court_id": r["court_id"], "date_filed": r["dateFiled"], "date_argued": r["dateArgued"],
			"docket_number": nullable(legalStripMarks(str(r, "docketNumber"))), "judges": nullable(legalStripMarks(str(r, "judge"))), "status": r["status"],
			"nature_of_suit": nullable(legalStripMarks(str(r, "suitNature"))), "snippet": legalNestedSnippet(r), "times_cited": legalDefault(r, "citeCount", 0), "url": legalPublicURL(str(r, "absolute_url"))})
	}
	return M{"results": rows, "count": legalDefault(data, "count", 0), "next_cursor": data["next"], "prev_cursor": data["previous"], "has_next": str(data, "next") != "", "has_prev": str(data, "previous") != ""}, nil
}

func (s *Server) legalResolveCase(ctx context.Context, user, id, kind string) (M, []M, error) {
	opinions := []M{}
	if kind == "cluster" {
		cluster, err := s.courtJSON(ctx, user, "/clusters/"+id+"/", nil)
		if err != nil {
			return nil, nil, err
		}
		refs := stringsOf(cluster["sub_opinions"])
		for _, ref := range refs[:min(len(refs), 6)] {
			if opID := legalResourceID(ref, "opinions"); opID != "" {
				if op, e := s.courtJSON(ctx, user, "/opinions/"+opID+"/", nil); e == nil {
					opinions = append(opinions, op)
				}
			}
		}
		return cluster, opinions, nil
	}
	op, err := s.courtJSON(ctx, user, "/opinions/"+id+"/", nil)
	if err != nil {
		return nil, nil, err
	}
	cluster := M{}
	if id := legalResourceID(str(op, "cluster"), "clusters"); id != "" {
		if c, e := s.courtJSON(ctx, user, "/clusters/"+id+"/", nil); e == nil {
			cluster = c
		}
	}
	return cluster, []M{op}, nil
}

func (s *Server) legalCaseDetail(ctx context.Context, user, id, kind string) (M, error) {
	order := []string{kind}
	if kind == "" {
		order = []string{"cluster", "opinion"}
	}
	var cluster M
	var raw []M
	resolved := ""
	for _, try := range order {
		c, ops, err := s.legalResolveCase(ctx, user, id, try)
		if err != nil {
			if courtMissing(err) {
				continue
			}
			return nil, err // An outage is not an absent ID in this namespace.
		}
		cluster, raw, resolved = c, ops, try
		break
	}
	if resolved == "" {
		return nil, fail(404, "Case not found")
	}
	docket := object(cluster["docket"])
	if id := legalResourceID(str(cluster, "docket"), "dockets"); id != "" {
		if d, e := s.courtJSON(ctx, user, "/dockets/"+id+"/", nil); e == nil {
			docket = d
		}
	}
	courtID := fallback(str(docket, "court_id"), legalResourceID(str(docket, "court"), "courts"))
	courtName, _ := s.legalCourtName(ctx, user, courtID).(string)
	if courtName == "" && !strings.HasPrefix(str(cluster, "court"), "http") {
		courtName = str(cluster, "court")
	}
	opinions := []M{}
	for _, op := range raw {
		text := str(op, "plain_text")
		if text == "" {
			text = courtText(fallback(str(op, "html_with_citations"), str(op, "html")))
		}
		code := str(op, "type")
		opinions = append(opinions, M{"id": op["id"], "type": code, "type_label": fallback(legalToolAssets.OpinionTypes[code], "Opinion"),
			"author": nullable(str(op, "author_str")), "per_curiam": op["per_curiam"] == true, "text": charSlice(text, 0, 50000)})
	}
	lead, leadRaw := M{}, M{}
	if len(opinions) > 0 {
		lead, leadRaw = opinions[0], raw[0]
	}
	citations := []string{}
	if list, ok := cluster["citations"].([]any); ok {
		for _, c := range list {
			if cite, ok := legalCitationString(c).(string); ok {
				citations = append(citations, cite)
			}
		}
	}
	idNumber, _ := strconv.ParseInt(id, 10, 64)
	return M{"id": idNumber, "resolved_as": resolved, "cluster_id": cluster["id"], "opinion_id": leadRaw["id"], "case_name": legalDefault(cluster, "case_name", "Unknown"),
		"case_name_short": cluster["case_name_short"], "case_name_full": cluster["case_name_full"], "citations": legalOrderCitations(citations), "court": courtName, "court_id": nullable(courtID),
		"date_filed": cluster["date_filed"], "date_argued": cluster["date_argued"], "docket_number": docket["docket_number"], "judges": legalDefault(cluster, "judges", ""),
		"nature_of_suit": legalDefault(cluster, "nature_of_suit", ""), "posture": str(cluster, "posture"), "opinion_text": str(lead, "text"),
		"html_text": charSlice(fallback(str(leadRaw, "html_with_citations"), str(leadRaw, "html")), 0, 100000), "opinions": opinions,
		"syllabus": legalDefault(cluster, "syllabus", ""), "headnotes": legalDefault(cluster, "headnotes", ""), "summary": legalDefault(cluster, "summary", ""),
		"times_cited": legalDefault(cluster, "citation_count", 0), "precedential_status": legalDefault(cluster, "precedential_status", ""), "url": legalPublicURL(str(cluster, "absolute_url"))}, nil
}
