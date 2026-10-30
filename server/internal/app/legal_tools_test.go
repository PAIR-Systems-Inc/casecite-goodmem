package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func legalOracle(t *testing.T) M {
	t.Helper()
	raw, err := os.ReadFile("testdata/legal_tools.json")
	if err != nil {
		t.Fatal(err)
	}
	data := M{}
	if err = json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if str(data, "baseline") != "6605cb3474cf0c2894080af216d5fed2a81fc997" {
		t.Fatal("unrecognized oracle baseline")
	}
	return data
}
func legalEqual(t *testing.T, got, want any) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(jsonBytes(got), &g); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(jsonBytes(want), &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("got %s\nwant %s", jsonBytes(got), jsonBytes(want))
	}
}

func TestLegalHelperPythonOracle(t *testing.T) {
	for i, fixture := range maps(legalOracle(t)["helpers"]) {
		t.Run(fmt.Sprintf("%s_%d", str(fixture, "function"), i), func(t *testing.T) {
			var got any
			raw := fixture["input"]
			switch str(fixture, "function") {
			case "strip_marks":
				text, _ := raw.(string)
				got = legalStripMarks(text)
			case "nested_snippet":
				got = legalNestedSnippet(object(raw))
			case "order_citations":
				got = legalOrderCitations(raw)
			case "citation_dict_to_string":
				got = legalCitationString(raw)
			case "_fmt_duration":
				got = legalDuration(raw)
			case "courts_for_jurisdiction":
				got = legalJurisdiction(raw.(string))
			case "resolve_court_ids":
				got = legalResolveCourts(stringsOf(raw))
			default:
				t.Fatal("unhandled oracle")
			}
			legalEqual(t, got, fixture["expected"])
		})
	}
}

func TestLegalFullServicePythonOracleAndRoutes(t *testing.T) {
	f := newFixture(t)
	admin := f.admin()
	oracle := legalOracle(t)
	fixtures := object(oracle["provider"])
	var mu sync.Mutex
	queries := []url.Values{}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token legal-fixture" {
			t.Error("missing user/instance token")
		}
		p := strings.TrimPrefix(r.URL.Path, "/api/rest/v4")
		q := r.URL.Query()
		mu.Lock()
		queries = append(queries, q)
		mu.Unlock()
		if p == "/search/" {
			if after := q.Get("filed_after"); after != "" {
				year, _ := strconv.Atoi(after[:4])
				writeJSON(w, 200, M{"count": map[int]int{2020: 100, 2021: 120, 2022: 150, 2023: 160}[year]})
				return
			}
			switch q.Get("type") {
			case "o":
				writeJSON(w, 200, fixtures["search_opinions"])
			case "d":
				writeJSON(w, 200, fixtures["search_dockets"])
			case "oa":
				writeJSON(w, 200, fixtures["search_audio"])
			default:
				t.Error("missing search type")
				w.WriteHeader(400)
			}
			return
		}
		if data, ok := fixtures[p]; ok {
			writeJSON(w, 200, data)
		} else {
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(provider.Close)
	f.s.cfg.CourtListenerURL = provider.URL + "/api/rest/v4"
	f.s.cfg.CourtListenerToken = "legal-fixture"
	f.s.court.interval = 0
	paths := []string{
		"/tools/cases/search?q=notice&page_size=20", "/tools/cases/77", "/tools/cases/55?kind=opinion", "/tools/cases/55",
		"/tools/precedents?topic=notice&jurisdiction=federal&min_citations=5&limit=20", "/tools/dockets?query=notice&court=ca9&party=Notice&limit=20",
		"/tools/dockets/8", "/tools/oral-arguments?query=notice&court=ca9&limit=50", "/tools/trends?topic=notice&start_year=2020&end_year=2023",
	}
	for i, c := range maps(oracle["calls"]) {
		t.Run(fmt.Sprintf("%s_%d", str(c, "function"), i), func(t *testing.T) {
			got := f.call("GET", paths[i], admin, nil, 200)
			if object(got["_api_timing"])["is_slow"] != false {
				t.Fatal("missing timing", got)
			}
			delete(got, "_api_timing")
			switch i {
			case 4:
				legalEqual(t, got, M{"precedents": c["expected"], "count": len(maps(c["expected"]))})
			case 5:
				legalEqual(t, got, M{"dockets": c["expected"], "count": len(maps(c["expected"]))})
			case 7:
				legalEqual(t, got, M{"arguments": c["expected"], "count": len(maps(c["expected"]))})
			default:
				legalEqual(t, got, c["expected"])
			}
		})
	}
	// A full, validated cursor is honored through the fixed API origin.
	cursor := provider.URL + "/api/rest/v4/search/?cursor=next&q=notice&type=o&page_size=25"
	f.call("GET", "/tools/cases/search?q=notice&cursor="+url.QueryEscape(cursor), admin, nil, 200)
	mu.Lock()
	defer mu.Unlock()
	foundPrecedent, foundParty, foundCursor := false, false, false
	for _, q := range queries {
		if q.Get("order_by") == "citeCount desc" {
			foundPrecedent = q.Get("court") == strings.Join(legalJurisdiction("federal"), ",") && q.Get("cited_gt") == "5" && q.Get("highlight") == "on"
		}
		if q.Get("type") == "d" {
			foundParty = q.Get("party_name") == "Notice" && q.Get("court") == "ca9"
		}
		if q.Get("cursor") == "next" {
			foundCursor = q.Get("page_size") == "25"
		}
	}
	if !foundPrecedent || !foundParty || !foundCursor {
		t.Fatal("query semantics changed", queries)
	}
}

func TestLegalRouteValidationPermissionsAndCredentials(t *testing.T) {
	f := newFixture(t)
	admin := f.admin()
	member := f.account(admin, "legal-reader@example.test")
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Token personal-court-key" {
			t.Error("personal credential not used")
		}
		writeJSON(w, 200, M{"results": []M{}, "count": 0})
	}))
	t.Cleanup(provider.Close)
	f.s.cfg.CourtListenerURL = provider.URL
	f.s.court.interval = 0
	paths := []string{"/tools/cases/search?q=x", "/tools/cases/1", "/tools/validate-citation?citation=x", "/tools/precedents?topic=x", "/tools/dockets?query=x", "/tools/dockets/1", "/tools/oral-arguments?query=x", "/tools/trends?topic=x"}
	for _, path := range paths {
		f.call("GET", path, "", nil, 401)
		f.call("GET", path, admin, nil, 503)
	}
	for _, path := range []string{"/tools/cases/search", "/tools/cases/search?q=x&page_size=0", "/tools/cases/search?q=x&page_size=101", "/tools/cases/search?q=x&page_size=foo", "/tools/cases/nope", "/tools/cases/1?kind=docket", "/tools/cases/1?kind=", "/tools/cases/-1", "/tools/validate-citation", "/tools/precedents", "/tools/precedents?topic=x&min_citations=-1", "/tools/precedents?topic=x&min_citations=1000001", "/tools/dockets?query=x&limit=101", "/tools/dockets", "/tools/dockets/nope", "/tools/oral-arguments", "/tools/oral-arguments?query=x&limit=0", "/tools/trends?topic=x&start_year=1599", "/tools/trends?topic=x&end_year=2201", "/tools/trends?topic=x&start_year=2025&end_year=2020"} {
		f.call("GET", path, admin, nil, 422)
	}
	if calls.Load() != 0 {
		t.Fatal("validation/missing credentials contacted provider")
	}
	f.call("PUT", "/keys/courtlistener", member, M{"api_key": "personal-court-key"}, 200)
	f.call("GET", paths[0], member, nil, 200)
	me := f.call("GET", "/auth/me", member, nil, 200)
	role := stringsOf(me["roles"])[0]
	if role == "admin" {
		t.Fatal("test member must not be admin")
	}
	f.call("PUT", "/admin/users/roles/"+role, admin, M{"permissions": []string{"documents.view"}}, 200)
	for _, path := range paths {
		f.call("GET", path, member, nil, 403)
	}
	if calls.Load() != 1 {
		t.Fatal("unauthorized request reached provider")
	}
}

func TestLegalCitationFullPythonAPIOracle(t *testing.T) {
	f := newFixture(t)
	admin := f.admin()
	f.s.cfg.CourtListenerToken = "fixture"
	f.s.court.interval = 0
	oracle := legalOracle(t)
	providerData := object(oracle["provider"])
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		switch {
		case strings.HasPrefix(q, "cites:"):
			writeJSON(w, 200, providerData["citing_opinions"])
		case strings.Contains(q, "999 U.S. 1"):
			writeJSON(w, 200, M{"results": []M{}})
		default:
			writeJSON(w, 200, providerData["citation_target"])
		}
	}))
	t.Cleanup(provider.Close)
	f.s.cfg.CourtListenerURL = provider.URL
	for _, c := range maps(oracle["citation_calls"]) {
		t.Run(str(c, "query"), func(t *testing.T) {
			result := f.call("GET", "/tools/validate-citation?citation="+url.QueryEscape(str(c, "query")), admin, nil, 200)
			delete(result, "_api_timing")
			legalEqual(t, result, c["expected"])
		})
	}
	for _, cite := range []string{"93 S. Ct. 705", "12 F. Supp. 3d 40", "10 L. Ed. 2d 20", "22 N.Y.S.3d 123", "2020 WL 12345", "410 U.S. 113"} {
		if !courtMatches("Example v. Other, "+cite+", 44 (2020)", M{"citation": []string{cite}}) {
			t.Error("multi-part reporter not recognized", cite)
		}
		if courtMatches(cite, M{"citation": []string{cite + "0"}}) {
			t.Error("different page matched", cite)
		}
	}
}

func TestLegalCaseIdentityOutagesAndSupplementalFailures(t *testing.T) {
	f := newFixture(t)
	admin := f.admin()
	f.s.cfg.CourtListenerToken = "fixture"
	f.s.court.interval = 0
	var primaryCode atomic.Int32
	var clusterCalls atomic.Int32
	var foreignCalls atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { foreignCalls.Add(1); w.WriteHeader(500) }))
	t.Cleanup(foreign.Close)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/opinions/9/":
			w.WriteHeader(404)
		case "/clusters/9/":
			clusterCalls.Add(1)
			if code := primaryCode.Load(); code != 0 {
				w.WriteHeader(int(code))
				return
			}
			writeJSON(w, 200, M{"id": 9, "case_name": "Only a cluster", "sub_opinions": []string{foreign.URL + "/opinions/55/"}, "docket": foreign.URL + "/dockets/8/"})
		case "/opinions/55/":
			writeJSON(w, 200, M{"id": 55, "plain_text": "Local authority text."})
		case "/dockets/8/":
			writeJSON(w, 200, M{"id": 8, "case_name": "Docket survives", "court": "https://www.courtlistener.com/api/rest/v4/courts/ca9/"})
		case "/parties/":
			writeJSON(w, 200, M{"results": []M{{"name": "Party"}}})
		case "/courts/ca9/":
			writeJSON(w, 200, M{"full_name": "Ninth Circuit"})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(provider.Close)
	f.s.cfg.CourtListenerURL = provider.URL
	f.call("GET", "/tools/cases/9?kind=opinion", admin, nil, 404)
	if clusterCalls.Load() != 0 {
		t.Fatal("explicit opinion fell back to an unrelated cluster")
	}
	got := f.call("GET", "/tools/cases/9", admin, nil, 200)
	if str(got, "resolved_as") != "cluster" || str(got, "opinion_text") != "Local authority text." {
		t.Fatal(got)
	}
	if foreignCalls.Load() != 0 {
		t.Fatal("provider reference leaked credentials to another origin")
	}
	primaryCode.Store(503)
	f.call("GET", "/tools/cases/9", admin, nil, 502)
	primaryCode.Store(403)
	f.call("GET", "/tools/cases/9", admin, nil, 502)
	got = f.call("GET", "/tools/dockets/8", admin, nil, 200)
	if len(maps(got["entries"])) != 0 || len(maps(got["parties"])) != 1 || str(got, "court_name") != "Ninth Circuit" {
		t.Fatal("independent metadata degradation failed", got)
	}
	f.call("GET", "/tools/dockets/999", admin, nil, 404)
}

func TestLegalCursorAndLinkBoundaries(t *testing.T) {
	s := &Server{}
	for _, cursor := range []string{"http://www.courtlistener.com/api/rest/v4/search/", "https://www.courtlistener.com.evil.test/api/rest/v4/search/", "https://user:password@www.courtlistener.com/api/rest/v4/search/", "https://www.courtlistener.com:444/api/rest/v4/search/", "https://www.courtlistener.com/api/rest/v4/opinions/", "https://www.courtlistener.com/api/rest/v4/search/%2e%2e/", "https://www.courtlistener.com/api/rest/v4/search/#x", "https://www.courtlistener.com/api/rest/v4/search/?type=d", "https://www.courtlistener.com/api/rest/v4/search/?page_size=10000", "/api/rest/v4/search/", "https://127.0.0.1/api/rest/v4/search/"} {
		if _, err := s.legalCursor(cursor); err == nil {
			t.Errorf("accepted unsafe cursor %s", cursor)
		}
	}
	for _, cursor := range []string{"https://www.courtlistener.com/api/rest/v4/search/?cursor=abc", "https://courtlistener.com:443/api/rest/v4/search/?cursor=next&type=o"} {
		if _, err := s.legalCursor(cursor); err != nil {
			t.Error(cursor, err)
		}
	}
	for _, link := range []string{"javascript:alert(1)", "data:text/html,bad", "//evil.test/a", "https://user:pass@host.test/doc"} {
		if legalPublicURL(link) != nil {
			t.Error("unsafe public link", link)
		}
	}
	if legalPublicURL("https://court.example.test/audio.mp3") == nil {
		t.Fatal("legitimate external court download rejected")
	}
	if legalResourceID("https://foreign.test/dockets/8/", "opinions") != "" {
		t.Fatal("resource namespace crossed")
	}
	got := legalTiming(M{}, 10555*time.Millisecond)
	if object(got["_api_timing"])["elapsed_seconds"] != 10.56 || got["_api_warning"] == nil {
		t.Fatal(got)
	}
}

func TestLegalTrendsPartialCacheCredentialsAndDirection(t *testing.T) {
	f := newFixture(t)
	admin := f.admin()
	f.s.cfg.CourtListenerToken = "fixture"
	f.s.court.interval = 0
	var calls atomic.Int32
	var failYear atomic.Bool
	failYear.Store(true)
	var denied atomic.Bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if denied.Load() {
			w.WriteHeader(403)
			return
		}
		if r.URL.Query().Get("filed_after") == "2020-01-01" && failYear.Load() {
			w.WriteHeader(404)
			return
		}
		writeJSON(w, 200, M{"count": 100})
	}))
	t.Cleanup(provider.Close)
	f.s.cfg.CourtListenerURL = provider.URL
	path := "/tools/trends?topic=notice&start_year=2020&end_year=2021"
	first := f.call("GET", path, admin, nil, 200)
	if first["years_failed"] != float64(1) || str(first, "trend") != "insufficient" {
		t.Fatal(first)
	}
	failYear.Store(false)
	second := f.call("GET", path, admin, nil, 200)
	if second["years_failed"] != float64(0) || str(second, "trend") != "stable" || calls.Load() != 4 {
		t.Fatal("partial sweep was cached", second, calls.Load())
	}
	f.call("GET", path, admin, nil, 200)
	if calls.Load() != 4 {
		t.Fatal("complete sweep was not cached")
	}
	// A new Server shares the PostgreSQL cache, with no Redis/process cache.
	other := &Server{cfg: f.s.cfg, db: f.s.db, client: f.s.client, court: newCourtGate()}
	user := str(f.call("GET", "/auth/me", admin, nil, 200), "id")
	if _, err := other.legalTrends(context.Background(), user, "notice", 2020, 2021); err != nil || calls.Load() != 4 {
		t.Fatal("cache did not survive server replacement", err)
	}
	f.call("PUT", "/keys/courtlistener", admin, M{"api_key": "rotated-token"}, 200)
	f.call("GET", path, admin, nil, 200)
	if calls.Load() != 6 {
		t.Fatal("changed credential reused old cache")
	}
	if _, err := f.s.db.Exec(`UPDATE cc_kv SET expires_at=now()-interval '1 second' WHERE key LIKE 'legal-trend:%'`); err != nil {
		t.Fatal(err)
	}
	denied.Store(true)
	f.call("GET", path, admin, nil, 502)
	result := legalTrendResult("notice", 2024, 2027, 2026, map[int]int{2024: 100, 2025: 120, 2026: 1, 2027: 0})
	if str(result, "trend") != "increasing" || object(result["trend_basis"])["to_year"] != 2025 || result["current_year_partial"] != true {
		t.Fatal("partial/future year produced false decline", result)
	}
}
