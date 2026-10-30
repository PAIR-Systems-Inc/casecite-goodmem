package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func judgeOracle(t *testing.T) M {
	t.Helper()
	raw, err := os.ReadFile("testdata/judge.json")
	if err != nil {
		t.Fatal(err)
	}
	data := M{}
	if err = json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if str(data, "baseline") != "6605cb3474cf0c2894080af216d5fed2a81fc997" {
		t.Fatal("unrecognized baseline")
	}
	return data
}
func TestJudgePythonOracle(t *testing.T) {
	oracle := judgeOracle(t)
	for i, c := range maps(oracle["helpers"]) {
		t.Run(fmt.Sprintf("%s_%d", str(c, "function"), i), func(t *testing.T) {
			args := judgeRefs(c["args"])
			var got any
			switch str(c, "function") {
			case "_judge_summary":
				got = judgeSummary(object(args[0]))
			case "_dissent_concurrence_metrics":
				a, b := judgeDissentMetrics(maps(args[0]))
				got = []M{a, b}
			case "_citation_impact_metrics":
				got = judgeCitationMetrics(maps(args[0]), 2026)
			case "_writing_complexity_metrics":
				got = judgeWritingMetrics(maps(args[0]))
			case "_opinion_length_trend_metrics":
				got = judgeLengthTrend(maps(args[0]))
			case "_docket_durations":
				got = judgeDurations(maps(args[0]))
			case "_case_disposition_metrics":
				got = judgeDurationMetrics(maps(args[0]))
			case "_case_type_metrics":
				got = judgeCaseTypeMetrics(maps(args[0]))
			case "_bench_experience_metrics":
				got = judgeBenchMetrics(maps(args[0]), 2026)
			case "title_matches_judge":
				got = judgeWikipediaTitle(args[0].(string), args[1].(string))
			case "intro_describes_judge":
				intro, _ := args[0].(string)
				year := 0
				if y, ok := args[1].(float64); ok {
					year = int(y)
				}
				got = judgeWikipediaIntro(intro, year)
			case "get_judge_profile":
				got = judgeProfile(object(oracle["judge"]), object(oracle["records"]))
			case "get_statistics":
				got = judgeStats(maps(object(oracle["records"])["opinions"]))
			default:
				t.Fatal("unhandled oracle")
			}
			want := c["expected"]
			switch str(c, "intentional_fix") {
			case "empty sample is insufficient":
				if str(object(want), "data_quality") != "limited" {
					t.Fatal("upstream changed")
				}
				object(want)["data_quality"] = "insufficient"
			case "even median averages middle pair":
				if judgeNumber(object(want), "median_days") != 20 {
					t.Fatal("upstream changed")
				}
				object(want)["median_days"] = 15
			}
			legalEqual(t, got, want)
		})
	}
	for i, c := range maps(oracle["queries"]) {
		t.Run(fmt.Sprintf("query_%d", i), func(t *testing.T) {
			q := object(c["query"])
			f := judgeOpinionFilter{Query: str(q, "query"), Court: str(q, "court"), Order: str(q, "order"), Start: number(q, "year_start", 0), End: number(q, "year_end", 0), MinCitations: number(q, "min_citations", 0), Limit: number(q, "limit", 50), Offset: number(q, "offset", 0)}
			legalEqual(t, judgeFilterOpinions(maps(object(oracle["records"])["opinions"]), f), c["expected"])
		})
	}
}
func TestJudgeCorrectedAttributionAndStatistics(t *testing.T) {
	ops := []M{{"opinion_type": "dissent", "authored_opinion_types": []string{"lead-opinion"}}, {"opinion_type": "dissent", "authored_opinion_types": []string{}}, {"authored_opinion_types": []string{"dissent", "concurrence-opinion"}}}
	d, c := judgeDissentMetrics(ops)
	if judgeNumber(d, "dissent_count") != 1 || judgeNumber(c, "concurrence_count") != 1 || judgeNumber(d, "total_opinions_analyzed") != 3 {
		t.Fatal(d, c)
	}
	positions := []M{{"position_type": "Judge", "court_name": "Appeals", "date_start": "2015-01-01"}, {"position_type": "Judge", "court_name": "District", "date_start": "2000-01-01", "date_termination": "2018-01-01"}, {"position_type": "Attorney", "date_start": "1980-01-01", "date_termination": "2000-01-01"}}
	metrics := judgeBenchMetrics(positions, 2026)
	if judgeNumber(metrics, "total_years") != 26 || str(object(metrics["current_position"]), "court") != "Appeals" || len(maps(metrics["positions"])) != 2 {
		t.Fatal(metrics)
	}
	if !judgeWikipediaTitle("John Roberts", "John G. Roberts Jr.") {
		t.Fatal("suffix confused with surname")
	}
	sections := judgeWikiSections("Intro\n== Early life ==\nBorn in Oakland.\n== Legal career ==\nServed the public.\n== Judicial service ==\nCourt of Appeals.")
	if str(sections, "early_life") != "Born in Oakland." || str(sections, "judicial_service") != "Court of Appeals." {
		t.Fatal(sections)
	}
	data := judgeMetrics("42", object(judgeOracle(t)["judge"]), object(judgeOracle(t)["records"]), time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC))
	if len(object(data["metrics"])) != 9 || len(object(data["methodology"])) != 9 || strings.Contains(string(jsonBytes(data["summary"])), "Higher than average") {
		t.Fatal(data)
	}
}

func judgeFixture(t *testing.T, hook func(http.ResponseWriter, *http.Request) bool) (*fixture, *atomic.Int64) {
	t.Helper()
	f := newFixture(t)
	providerData := object(judgeOracle(t)["provider"])
	calls := &atomic.Int64{}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wiki" {
			if r.Header.Get("Authorization") != "" {
				t.Error("court token sent to Wikipedia")
			}
			if r.URL.Query().Get("list") == "search" {
				writeJSON(w, 200, M{"query": M{"search": []M{{"title": "Jane Example"}}}})
			} else {
				text := "Jane Example (born 1965) is an American judge."
				if r.URL.Query().Get("exintro") == "" {
					text += "\n== Early life ==\nBorn in Oakland.\n== Education ==\nExample University."
				}
				writeJSON(w, 200, M{"query": M{"pages": M{"1": M{"extract": text}}}})
			}
			return
		}
		calls.Add(1)
		if r.Header.Get("Authorization") != "Token judge-fixture" {
			t.Errorf("wrong CourtListener token: %q", r.Header.Get("Authorization"))
		}
		if hook != nil && hook(w, r) {
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/api/rest/v4")
		key := path
		if path == "/people/" {
			key = "search_people"
		}
		if path == "/search/" {
			key = map[string]string{"p": "search_people", "o": "search_opinions", "d": "search_dockets"}[r.URL.Query().Get("type")]
		}
		if data, ok := providerData[key]; ok {
			writeJSON(w, 200, data)
		} else {
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(provider.Close)
	f.s.cfg.CourtListenerURL = provider.URL + "/api/rest/v4"
	f.s.cfg.CourtListenerToken = "judge-fixture"
	f.s.cfg.WikipediaURL = provider.URL + "/wiki"
	f.s.court.interval = 0
	return f, calls
}
func TestJudgeBuildCacheAPIAndIdentity(t *testing.T) {
	var mu sync.Mutex
	queries := []url.Values{}
	f, calls := judgeFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		mu.Lock()
		queries = append(queries, r.URL.Query())
		mu.Unlock()
		return false
	})
	admin := f.admin()
	member := f.account(admin, "judge-member@example.test")
	f.call("GET", "/judge-intel/search?q=Jane%20Example", "", nil, 401)
	search := f.call("GET", "/judge-intel/search?q=Jane%20Example", admin, nil, 200)
	if number(search, "count", 0) != 1 || str(maps(search["judges"])[0], "name") != "Jane Example" {
		t.Fatal(search)
	}
	job := f.call("POST", "/judge-intel/build/42?async=true", admin, nil, 202)
	duplicate := f.call("POST", "/judge-intel/build/42?async=true", admin, nil, 202)
	if str(job, "job_id") != str(duplicate, "job_id") {
		t.Fatal("duplicate work admitted")
	}
	f.call("POST", "/judge-intel/build/42?async=true", member, nil, 409)
	f.call("GET", "/judge-intel/profile/42", admin, nil, 404)
	result := runReviewJob(t, f, admin, str(job, "job_id"))
	profile := object(result["profile"])
	if str(result, "status") != "complete" || str(profile, "name") != "Jane Example" || len(maps(profile["education"])) != 1 || len(maps(profile["positions"])) != 1 || str(profile, "wikipedia_early_life") != "Born in Oakland." {
		t.Fatal(result)
	}
	before := calls.Load()
	cached := f.call("POST", "/judge-intel/build/42", member, nil, 200)
	if str(cached, "status") != "cached" || calls.Load() != before {
		t.Fatal("cache hit contacted provider")
	}
	f.call("GET", "/judge-intel/profile/42", member, nil, 200)
	ops := f.call("GET", "/judge-intel/profile/42/opinions?order=citations", member, nil, 200)
	if number(ops, "total", 0) != 1 || !strings.Contains(str(maps(ops["opinions"])[0], "snippet"), "dissent") {
		t.Fatal(ops)
	}
	text := f.call("GET", "/judge-intel/profile/42/opinion/77", member, nil, 200)
	oracle := object(judgeOracle(t)["cluster_text"])
	legalEqual(t, text["full_text"], oracle["text"])
	legalEqual(t, text["opinion_parts"], oracle["parts"])
	f.call("GET", "/judge-intel/profile/42/opinion/55", member, nil, 404)
	metrics := f.call("GET", "/judge-intel/profile/42/metrics", member, nil, 200)
	if number(object(object(metrics["metrics"])["dissent_rate"]), "dissent_count", -1) != 0 {
		t.Fatal("other judge's dissent attributed to this judge", metrics)
	}
	stats := f.call("GET", "/judge-intel/profile/42/stats", member, nil, 200)
	if number(object(stats["overview"]), "total_opinions", 0) != 1 {
		t.Fatal(stats)
	}
	again := f.call("GET", "/judge-intel/profile/42/metrics", member, nil, 200)
	if again["computed_at"] != metrics["computed_at"] {
		t.Fatal("metrics cache miss")
	}
	mu.Lock()
	defer mu.Unlock()
	sawCitation := false
	sawDockets := false
	for _, q := range queries {
		if id := q.Get("citing_opinion"); id != "" {
			sawCitation = true
			if id != "55" {
				t.Fatal("wrong citation identity", id)
			}
		}
		if q.Get("type") == "o" && (!strings.Contains(q.Get("q"), "author_id:42") || strings.Contains(q.Get("q"), `judge:"Example"`)) {
			t.Fatal("unsafe author query", q)
		}
		if q.Get("type") == "d" {
			sawDockets = true
			if !strings.Contains(q.Get("q"), "assigned_to_id:42") {
				t.Fatal(q)
			}
		}
	}
	if !sawCitation || !sawDockets {
		t.Fatal("missing provider calls")
	}
	f.call("DELETE", "/judge-intel/profile/42", member, nil, 403)
	f.call("DELETE", "/judge-intel/profile/42", admin, nil, 200)
	f.call("GET", "/judge-intel/profile/42", admin, nil, 404)
}
func TestJudgeValidationAndQueueRollback(t *testing.T) {
	f, calls := judgeFixture(t, nil)
	admin := f.admin()
	for _, path := range []string{"/search", "/search?q=x&limit=0", "/search?q=x&limit=1001", "/profile/0", "/profile/42/opinions?limit=201", "/profile/42/opinions?offset=-1", "/profile/42/opinions?order=name", "/profile/42/opinions?year_start=2025&year_end=2020", "/profile/42/metrics?force_recompute=perhaps", "/profile/42/opinion/x"} {
		f.call("GET", "/judge-intel"+path, admin, nil, 422)
	}
	f.call("POST", "/judge-intel/build/x", admin, nil, 422)
	f.call("POST", "/judge-intel/build/42?async=perhaps", admin, nil, 422)
	if calls.Load() != 0 {
		t.Fatal("invalid requests reached provider")
	}
	account := f.call("GET", "/auth/me", admin, nil, 200)
	for i := 0; i < 5; i++ {
		if _, err := f.s.db.Exec(`INSERT INTO cc_jobs(id,user_id,kind,input) VALUES($1,$2,'research','{}')`, uuid(), str(account, "id")); err != nil {
			t.Fatal(err)
		}
	}
	f.call("POST", "/judge-intel/build/42?async=true", admin, nil, 429)
	var n int
	if err := f.s.db.QueryRow(`SELECT count(*) FROM cc_judge_cache`).Scan(&n); err != nil || n != 0 {
		t.Fatal("queue rejection left cache reservation", n, err)
	}
}
func TestJudgeCancellationRefreshAndDeletion(t *testing.T) {
	for _, action := range []string{"cancel", "delete", "lease", "permission"} {
		t.Run(action, func(t *testing.T) {
			var block atomic.Bool
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			f, _ := judgeFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
				if strings.HasSuffix(r.URL.Path, "/people/42/") && block.Load() {
					once.Do(func() { close(entered) })
					<-release
				}
				return false
			})
			admin := f.admin()
			member := f.account(admin, "judge-worker@example.test")
			first := f.call("POST", "/judge-intel/build/42?async=true", member, nil, 202)
			runReviewJob(t, f, member, str(first, "job_id"))
			old := f.call("GET", "/judge-intel/profile/42", member, nil, 200)
			block.Store(true)
			refresh := f.call("POST", "/judge-intel/build/42?async=true&force_refresh=true", member, nil, 202)
			jobID := str(refresh, "job_id")
			work := claimReviewJob(t, f, jobID)
			done := make(chan struct{})
			go func() { defer close(done); work() }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				close(release)
				t.Fatal("worker did not enter provider")
			}
			still := f.call("GET", "/judge-intel/profile/42", admin, nil, 200)
			if still["data_pulled_at"] != old["data_pulled_at"] {
				t.Fatal("refresh removed cached profile")
			}
			switch action {
			case "cancel":
				f.call("DELETE", "/jobs/"+jobID, member, nil, 200)
			case "delete":
				f.call("DELETE", "/judge-intel/profile/42", admin, nil, 200)
			case "lease":
				if _, err := f.s.db.Exec(`UPDATE cc_jobs SET status='failed',error='Worker lease expired' WHERE id=$1`, jobID); err != nil {
					t.Fatal(err)
				}
			case "permission":
				if err := f.s.kvSet(context.Background(), "permissions:attorney", []string{"documents.view"}, 0); err != nil {
					t.Fatal(err)
				}
			}
			close(release)
			select {
			case <-done:
			case <-time.After(8 * time.Second):
				t.Fatal("worker did not finish")
			}
			if action == "delete" {
				f.call("GET", "/judge-intel/profile/42", admin, nil, 404)
			} else {
				after := f.call("GET", "/judge-intel/profile/42", admin, nil, 200)
				if after["data_pulled_at"] != old["data_pulled_at"] {
					t.Fatal("failed build published")
				}
			}
			var count int
			if err := f.s.db.QueryRow(`SELECT count(*) FROM cc_objects WHERE id=$1`, jobID).Scan(&count); err != nil || count != 0 {
				t.Fatal("cancelled build left output", count, err)
			}
		})
	}
}

func TestJudgeLazyTextConcurrentCacheAndMetricsExpiry(t *testing.T) {
	var blocked atomic.Bool
	arrived, release := make(chan struct{}, 2), make(chan struct{})
	f, _ := judgeFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if strings.Contains(r.URL.Path, "/clusters/") && blocked.Load() {
			arrived <- struct{}{}
			<-release
		}
		if strings.HasSuffix(r.URL.Path, "/clusters/78/") {
			writeJSON(w, 200, object(judgeOracle(t)["provider"])["/clusters/77/"])
			return true
		}
		return false
	})
	admin := f.admin()
	job := f.call("POST", "/judge-intel/build/42?async=true", admin, nil, 202)
	runReviewJob(t, f, admin, str(job, "job_id"))
	cache, err := f.s.judgeCache(context.Background(), "42")
	if err != nil {
		t.Fatal(err)
	}
	op := maps(cache.Records["opinions"])[0]
	op["full_text"] = ""
	second := judgePick(op)
	for k, v := range op {
		second[k] = v
	}
	second["id"] = 78
	cache.Records["opinions"] = []M{op, second}
	if _, err = f.s.db.Exec(`UPDATE cc_judge_cache SET records=$1,metrics=NULL WHERE judge_id=42`, jsonBytes(cache.Records)); err != nil {
		t.Fatal(err)
	}
	blocked.Store(true)
	done := make(chan struct{}, 2)
	for _, id := range []string{"77", "78"} {
		go func(id string) {
			defer func() { done <- struct{}{} }()
			f.call("GET", "/judge-intel/profile/42/opinion/"+id, admin, nil, 200)
		}(id)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-arrived:
		case <-time.After(5 * time.Second):
			close(release)
			t.Fatal("concurrent text requests did not reach provider")
		}
	}
	close(release)
	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("text request did not complete")
		}
	}
	cache, err = f.s.judgeCache(context.Background(), "42")
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range maps(cache.Records["opinions"]) {
		if !strings.Contains(str(op, "full_text"), "notice provision") {
			t.Fatal("concurrent cache write lost text", op)
		}
	}
	metrics := f.call("GET", "/judge-intel/profile/42/metrics", admin, nil, 200)
	for _, stamp := range []string{time.Now().Add(-25 * time.Hour).Format(time.RFC3339), time.Now().Add(time.Hour).Format(time.RFC3339)} {
		metrics["computed_at"] = stamp
		if _, err = f.s.db.Exec(`UPDATE cc_judge_cache SET metrics=$1 WHERE judge_id=42`, jsonBytes(metrics)); err != nil {
			t.Fatal(err)
		}
		fresh := f.call("GET", "/judge-intel/profile/42/metrics", admin, nil, 200)
		if str(fresh, "computed_at") == stamp {
			t.Fatal("expired/future metrics reused")
		}
	}
}
func TestJudgePagingErrorsAndSynchronousStatus(t *testing.T) {
	var mode atomic.Int32
	f, calls := judgeFixture(t, func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasSuffix(r.URL.Path, "/people/") && mode.Load() == 1 {
			writeJSON(w, 200, M{"count": 5, "results": []M{{"id": 42, "name_first": "Jane", "name_last": "Example"}}, "next": "https://unrelated.invalid/api/rest/v4/people/?page=2"})
			return true
		}
		if strings.HasSuffix(r.URL.Path, "/search/") && mode.Load() == 2 {
			w.WriteHeader(401)
			return true
		}
		return false
	})
	admin := f.admin()
	mode.Store(1)
	f.call("GET", "/judge-intel/search?q=Jane%20Example", admin, nil, 502)
	if calls.Load() != 1 {
		t.Fatal("invalid pagination caused another request", calls.Load())
	}
	mode.Store(2)
	job := f.call("POST", "/judge-intel/build/42?async=true", admin, nil, 202)
	claimReviewJob(t, f, str(job, "job_id"))()
	failed := f.call("GET", "/jobs/"+str(job, "job_id"), admin, nil, 200)
	if str(failed, "status") != "failed" {
		t.Fatal(failed)
	}
	f.call("GET", "/judge-intel/profile/42", admin, nil, 404)
	mode.Store(0)
	f.s.StartWorkers()
	f.call("POST", "/judge-intel/build/999", admin, nil, 404)
	completed := f.call("POST", "/judge-intel/build/42", admin, nil, 200)
	if str(completed, "status") != "complete" {
		t.Fatal(completed)
	}
}
