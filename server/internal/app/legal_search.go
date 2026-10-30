package app

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

func (s *Server) legalPrecedents(ctx context.Context, user, topic, jurisdiction string, minCitations, limit int) ([]M, error) {
	params := url.Values{"q": {topic}, "type": {"o"}, "cited_gt": {strconv.Itoa(minCitations)}, "order_by": {"citeCount desc"}, "page_size": {strconv.Itoa(limit)}, "highlight": {"on"}}
	if courts := legalResolveCourts([]string{jurisdiction}); len(courts) > 0 {
		params.Set("court", strings.Join(courts, ","))
	}
	data, err := s.courtJSON(ctx, user, "/search/", params)
	if err != nil {
		return nil, err
	}
	out := []M{}
	for _, r := range maps(data["results"]) {
		citations := legalOrderCitations(r["citation"])
		citation := "No citation"
		if len(citations) > 0 {
			citation = citations[0]
		}
		id := r["cluster_id"]
		if legalScalar(id) == "" {
			id = legalDefault(r, "id", 0)
		}
		out = append(out, M{"id": id, "opinion_id": legalFirstOpinion(r), "case_name": fallback(legalStripMarks(str(r, "caseName")), "Unknown"),
			"citation": citation, "citations": citations, "court": legalDefault(r, "court", ""), "court_id": r["court_id"], "date_filed": legalDate(r["dateFiled"]),
			"docket_number": nullable(legalStripMarks(str(r, "docketNumber"))), "judges": nullable(legalStripMarks(str(r, "judge"))), "status": r["status"],
			"times_cited": legalDefault(r, "citeCount", 0), "snippet": nullable(legalNestedSnippet(r)), "url": legalURLOrHome(str(r, "absolute_url"))})
	}
	return out, nil
}

func (s *Server) legalDockets(ctx context.Context, user, query, court, party string, limit int) ([]M, error) {
	params := url.Values{"q": {query}, "type": {"d"}, "page_size": {strconv.Itoa(limit)}}
	if court != "" {
		params.Set("court", court)
	}
	if party != "" {
		params.Set("party_name", party)
	}
	data, err := s.courtJSON(ctx, user, "/search/", params)
	if err != nil {
		return nil, err
	}
	out := []M{}
	for _, r := range maps(data["results"]) {
		id := r["docket_id"]
		if legalScalar(id) == "" {
			id = legalDefault(r, "id", 0)
		}
		out = append(out, M{"id": id, "case_name": legalDefault(r, "caseName", "Unknown"), "case_name_full": r["case_name_full"], "docket_number": legalDefault(r, "docketNumber", ""),
			"court": legalDefault(r, "court", ""), "court_id": r["court_id"], "date_filed": legalDate(r["dateFiled"]), "date_terminated": legalDate(r["dateTerminated"]), "date_argued": legalDate(r["dateArgued"]),
			"nature_of_suit": nullable(str(r, "suitNature")), "cause": nullable(str(r, "cause")), "jury_demand": nullable(str(r, "juryDemand")), "jurisdiction_type": nullable(str(r, "jurisdictionType")),
			"assigned_to": nullable(str(r, "assignedTo")), "referred_to": nullable(str(r, "referredTo")), "parties": legalNames(r["party"]), "attorneys": legalNames(r["attorney"]),
			"entry_count": legalDefault(r, "entry_count", 0), "url": legalURLOrHome(fallback(str(r, "docket_absolute_url"), str(r, "absolute_url")))})
	}
	return out, nil
}

func (s *Server) legalDocketDetail(ctx context.Context, user, id string) (M, error) {
	docket, err := s.courtJSON(ctx, user, "/dockets/"+id+"/", nil)
	if err != nil {
		if courtMissing(err) {
			return nil, fail(404, "Docket not found")
		}
		return nil, err
	}
	entries, parties := []M{}, []M{}
	entryCount := 0
	if data, e := s.courtJSON(ctx, user, "/docket-entries/", url.Values{"docket": {id}, "order_by": {"-date_filed"}, "page_size": {"100"}}); e == nil {
		entryCount = number(data, "count", 0)
		for _, r := range maps(data["results"]) {
			docs := maps(r["recap_documents"])
			description := str(r, "description")
			if description == "" && len(docs) > 0 {
				description = str(docs[0], "description")
			}
			documents := []M{}
			for _, d := range docs {
				documents = append(documents, M{"description": str(d, "description"), "document_number": d["document_number"], "page_count": d["page_count"], "url": legalPublicURL(str(d, "filepath_local")), "is_available": d["is_available"] == true})
			}
			entries = append(entries, M{"entry_number": r["entry_number"], "date_filed": r["date_filed"], "description": description, "documents": documents})
		}
	}
	if data, e := s.courtJSON(ctx, user, "/parties/", url.Values{"docket": {id}, "page_size": {"100"}}); e == nil {
		for _, p := range maps(data["results"]) {
			var role any
			if types := maps(p["party_types"]); len(types) > 0 {
				role = types[0]["name"]
			}
			attorneys := []string{}
			for _, a := range maps(p["attorneys"]) {
				if name := str(a, "name"); name != "" {
					attorneys = append(attorneys, name)
				}
			}
			parties = append(parties, M{"name": p["name"], "role": role, "attorneys": attorneys})
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	courtID := fallback(str(docket, "court_id"), legalResourceID(str(docket, "court"), "courts"))
	idNumber, _ := strconv.ParseInt(id, 10, 64)
	if entryCount == 0 {
		entryCount = len(entries)
	}
	return M{"id": idNumber, "case_name": legalDefault(docket, "case_name", "Unknown"), "case_name_full": nullable(str(docket, "case_name_full")), "docket_number": legalDefault(docket, "docket_number", ""),
		"court": courtID, "court_name": s.legalCourtName(ctx, user, courtID), "date_filed": docket["date_filed"], "date_terminated": docket["date_terminated"], "date_last_filing": docket["date_last_filing"], "date_argued": docket["date_argued"],
		"assigned_to": nullable(str(docket, "assigned_to_str")), "referred_to": nullable(str(docket, "referred_to_str")), "nature_of_suit": nullable(str(docket, "nature_of_suit")), "cause": nullable(str(docket, "cause")),
		"jury_demand": nullable(str(docket, "jury_demand")), "jurisdiction_type": nullable(str(docket, "jurisdiction_type")), "pacer_case_id": nullable(str(docket, "pacer_case_id")),
		"entries": entries, "entry_count": entryCount, "parties": parties, "url": legalURLOrHome(str(docket, "absolute_url"))}, nil
}

func (s *Server) legalOralArguments(ctx context.Context, user, query, court string, limit int) ([]M, error) {
	params := url.Values{"q": {query}, "type": {"oa"}, "page_size": {strconv.Itoa(limit)}, "highlight": {"on"}}
	if court != "" {
		params.Set("court", court)
	}
	data, err := s.courtJSON(ctx, user, "/search/", params)
	if err != nil {
		return nil, err
	}
	out := []M{}
	for _, r := range maps(data["results"]) {
		var audio any
		local := fallback(str(r, "local_path_mp3"), str(r, "local_path"))
		if local != "" {
			if !strings.Contains(local, ":") && !strings.HasPrefix(local, "//") {
				local = "https://storage.courtlistener.com/" + strings.TrimLeft(local, "/")
			}
			if strings.HasPrefix(local, "https://") {
				audio = legalPublicURL(local)
			}
		}
		download := legalPublicURL(str(r, "download_url"))
		if audio == nil && strings.HasPrefix(str(r, "download_url"), "https://") {
			audio = download
		}
		out = append(out, M{"id": r["id"], "case_name": fallback(legalStripMarks(str(r, "caseName")), "Unknown"), "case_name_full": nullable(legalStripMarks(str(r, "case_name_full"))),
			"court": legalDefault(r, "court", ""), "court_id": r["court_id"], "date_argued": r["dateArgued"], "docket_number": nullable(legalStripMarks(str(r, "docketNumber"))),
			"judges": nullable(legalStripMarks(str(r, "judge"))), "duration": r["duration"], "duration_label": legalDuration(r["duration"]), "audio_url": audio, "download_url": download,
			"snippet": nullable(legalStripMarks(str(r, "snippet"))), "url": legalURLOrHome(str(r, "absolute_url")), "docket_id": r["docket_id"]})
	}
	return out, nil
}
