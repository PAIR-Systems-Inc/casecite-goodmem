package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

type judgeCache struct {
	Generation                string
	Profile, Records, Metrics M
	Updated                   time.Time
}

func (s *Server) judgeCache(ctx context.Context, id string) (judgeCache, error) {
	var c judgeCache
	var profile, records, metrics []byte
	err := s.db.QueryRowContext(ctx, `SELECT generation,profile,records,metrics,updated_at FROM cc_judge_cache WHERE judge_id=$1 AND profile IS NOT NULL`, id).Scan(&c.Generation, &profile, &records, &metrics, &c.Updated)
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(profile, &c.Profile); err != nil {
		return c, err
	}
	if err = json.Unmarshal(records, &c.Records); err != nil {
		return c, err
	}
	if len(metrics) > 0 {
		err = json.Unmarshal(metrics, &c.Metrics)
	}
	return c, err
}
func judgeQueryBool(q request, key string) (bool, error) {
	if !q.r.URL.Query().Has(key) {
		return false, nil
	}
	value, valid := researchBool(q.r.URL.Query().Get(key))
	if !valid {
		return false, fail(422, key+" must be a boolean")
	}
	return value, nil
}
func (s *Server) judgeRoutes() {
	s.api("GET /api/v1/judge-intel/search", false, func(q request) (any, error) {
		query, err := legalQueryRequired(q, "q")
		if err != nil {
			return nil, err
		}
		limit, err := legalQueryInt(q, "limit", 500, 1, 1000)
		if err != nil {
			return nil, err
		}
		if len([]rune(query)) > 300 {
			return nil, fail(422, "Judge name exceeds 300 characters")
		}
		if strings.TrimSpace(query) == "" {
			return M{"judges": []M{}, "count": 0, "total_available": 0}, nil
		}
		if err = s.requireCourtToken(q.r.Context(), q.user.ID); err != nil {
			return nil, err
		}
		return s.judgeSearch(q.r.Context(), q.user.ID, query, limit)
	})
	s.api("POST /api/v1/judge-intel/build/{id}", false, s.judgeBuild)
	s.api("GET /api/v1/judge-intel/profile/{id}", false, func(q request) (any, error) {
		id, err := legalPathID(q)
		if err != nil {
			return nil, err
		}
		c, err := s.judgeCache(q.r.Context(), id)
		if notFound(err) {
			return nil, fail(404, "Judge not found in local database. Use /build first.")
		}
		if err != nil {
			return nil, err
		}
		return judgeProfile(c.Profile, c.Records), nil
	})
	s.api("GET /api/v1/judge-intel/profile/{id}/opinions", false, func(q request) (any, error) {
		id, err := legalPathID(q)
		if err != nil {
			return nil, err
		}
		f := judgeOpinionFilter{Query: q.r.URL.Query().Get("q"), Court: q.r.URL.Query().Get("court"), Order: "date"}
		if q.r.URL.Query().Has("order") {
			f.Order = q.r.URL.Query().Get("order")
			if f.Order != "date" && f.Order != "citations" {
				return nil, fail(422, "order must be date or citations")
			}
		}
		for _, field := range []struct {
			key            string
			target         *int
			def, low, high int
		}{{"limit", &f.Limit, 50, 1, 200}, {"offset", &f.Offset, 0, 0, 100000}, {"year_start", &f.Start, 0, 1, 9999}, {"year_end", &f.End, 0, 1, 9999}, {"min_citations", &f.MinCitations, 0, 0, 1000000000}} {
			*field.target, err = legalQueryInt(q, field.key, field.def, field.low, field.high)
			if err != nil {
				return nil, err
			}
		}
		if f.Start > 0 && f.End > 0 && f.Start > f.End {
			return nil, fail(422, "year_end must be at least year_start")
		}
		c, err := s.judgeCache(q.r.Context(), id)
		if err != nil && !notFound(err) {
			return nil, err
		}
		return judgeFilterOpinions(maps(c.Records["opinions"]), f), nil
	})
	s.api("GET /api/v1/judge-intel/profile/{id}/opinion/{opinion}", false, s.judgeOpinion)
	s.api("GET /api/v1/judge-intel/profile/{id}/stats", false, func(q request) (any, error) {
		id, err := legalPathID(q)
		if err != nil {
			return nil, err
		}
		c, err := s.judgeCache(q.r.Context(), id)
		if notFound(err) {
			return M{}, nil
		}
		if err != nil {
			return nil, err
		}
		return judgeStats(maps(c.Records["opinions"])), nil
	})
	s.api("GET /api/v1/judge-intel/profile/{id}/metrics", false, s.judgeMetricsAPI)
	s.api("DELETE /api/v1/judge-intel/profile/{id}", false, s.judgeDelete)
}
func (s *Server) judgeBuild(q request) (any, error) {
	id, err := legalPathID(q)
	if err != nil {
		return nil, err
	}
	force, err := judgeQueryBool(q, "force_refresh")
	if err != nil {
		return nil, err
	}
	async, err := judgeQueryBool(q, "async")
	if err != nil {
		return nil, err
	}
	ctx := q.r.Context()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Queue admission and per-judge build reservation commit together. Separate
	// readers keep the previous complete profile while a refresh is running.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,1))`, q.user.ID); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,19))`, id); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO cc_judge_cache(judge_id,generation) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, uuid()); err != nil {
		return nil, err
	}
	var profile, records []byte
	var existing sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT profile,records,build_job_id FROM cc_judge_cache WHERE judge_id=$1 FOR UPDATE`, id).Scan(&profile, &records, &existing)
	if err != nil {
		return nil, err
	}
	if len(profile) > 0 && !force {
		var p, r M
		if err = json.Unmarshal(profile, &p); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(records, &r); err != nil {
			return nil, err
		}
		return M{"status": "cached", "message": "Judge data already exists in database. Use force_refresh=true to rebuild.", "profile": judgeProfile(p, r)}, nil
	}
	jobID := ""
	if existing.Valid {
		var status, owner string
		err = tx.QueryRowContext(ctx, `SELECT status,user_id FROM cc_jobs WHERE id=$1`, existing.String).Scan(&status, &owner)
		if err != nil && !notFound(err) {
			return nil, err
		}
		if status == "running" || status == "pending" {
			if owner != q.user.ID {
				return nil, fail(409, "This judge's profile is already being built. Try opening it again shortly.")
			}
			jobID = existing.String
		}
	}
	if jobID == "" {
		if err = s.requireCourtToken(ctx, q.user.ID); err != nil {
			return nil, err
		}
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM cc_jobs WHERE user_id=$1 AND status IN ('pending','running')`, q.user.ID).Scan(&count); err != nil {
			return nil, err
		}
		if count >= 5 {
			return nil, fail(429, "Five jobs are already queued. Wait or cancel a job.")
		}
		jobID = uuid()
		generation := uuid()
		input := M{"judge_id": id, "generation": generation, "document_ids": []string{}}
		if _, err = tx.ExecContext(ctx, `INSERT INTO cc_jobs(id,user_id,kind,input) VALUES($1,$2,'judge.build',$3)`, jobID, q.user.ID, jsonBytes(input)); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE cc_judge_cache SET build_job_id=$2,generation=$3 WHERE judge_id=$1`, id, jobID, generation); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if async {
		writeJSON(q.w, 202, M{"job_id": jobID, "poll_url": "/api/v1/jobs/" + jobID})
		return nil, nil
	}
	// The same durable job supports synchronous callers. A disconnected browser
	// can reconnect to the build instead of wasting the completed provider work.
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		var status string
		var result []byte
		var progress []byte
		var message sql.NullString
		err = s.db.QueryRowContext(ctx, `SELECT status,result,error,progress FROM cc_jobs WHERE id=$1 AND user_id=$2`, jobID, q.user.ID).Scan(&status, &result, &message, &progress)
		if err != nil {
			return nil, err
		}
		if status == "completed" {
			out := M{}
			if err = json.Unmarshal(result, &out); err != nil {
				return nil, err
			}
			return out, nil
		}
		if status == "failed" || status == "cancelled" {
			p := M{}
			_ = json.Unmarshal(progress, &p)
			return nil, fail(number(p, "error_status", 502), fallback(message.String, "Judge profile build did not complete"))
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

type jobJudge struct {
	ID, Generation   string
	Profile, Records M
}

func (s *Server) executeJudgeBuild(ctx context.Context, user string, input M) (M, error) {
	check := func() error {
		account, err := s.loadUser(ctx, user)
		if err != nil {
			return err
		}
		if !slices.Contains(account.Permissions, "judge_intel.use") {
			return fail(403, "Judge intelligence access is no longer available")
		}
		return nil
	}
	if err := check(); err != nil {
		return nil, err
	}
	id := str(input, "judge_id")
	data, records, err := s.judgeBuildData(ctx, user, id)
	if err != nil {
		return nil, err
	}
	if err = check(); err != nil {
		return nil, err
	}
	outputs, ok := ctx.Value(jobOutputsKey{}).(*jobOutputs)
	if !ok {
		return nil, fmt.Errorf("judge build requires a durable job")
	}
	outputs.Lock()
	outputs.Judges = append(outputs.Judges, jobJudge{id, str(input, "generation"), data, records})
	outputs.Unlock()
	s.jobAudit(ctx, user, "judge.build", "judge", id, M{"opinions": len(maps(records["opinions"])), "dockets": len(maps(records["dockets"]))})
	return M{"status": "complete", "message": "Judge intelligence profile built successfully", "profile": judgeProfile(data, records)}, nil
}
func (s *Server) judgeOpinion(q request) (any, error) {
	id, err := legalPathID(q)
	if err != nil {
		return nil, err
	}
	n, err := strconv.ParseInt(q.r.PathValue("opinion"), 10, 64)
	if err != nil || n <= 0 {
		return nil, fail(422, "opinion must be a positive integer")
	}
	opID := strconv.FormatInt(n, 10)
	ctx := q.r.Context()
	cache, err := s.judgeCache(ctx, id)
	if err != nil {
		return nil, err
	}
	var op M
	for _, row := range maps(cache.Records["opinions"]) {
		if courtID(row["id"]) == opID {
			op = row
			break
		}
	}
	if op == nil {
		return nil, fail(404, "Opinion not found")
	}
	if strings.TrimSpace(str(op, "full_text")) != "" && op["text_partial"] != true {
		return op, nil
	}
	if err = s.requireCourtToken(ctx, q.user.ID); err != nil {
		op["text_unavailable"] = true
		op["text_error"] = "Configure a CourtListener token to retrieve this opinion's text."
		return op, nil
	}
	text, err := s.judgeClusterText(ctx, q.user.ID, opID, id)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var generation string
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT generation,records FROM cc_judge_cache WHERE judge_id=$1 FOR UPDATE`, id).Scan(&generation, &raw); err != nil {
		return nil, err
	}
	if generation != cache.Generation {
		return nil, fail(409, "Judge data changed during retrieval. Reopen the opinion.")
	}
	var current M
	if err = json.Unmarshal(raw, &current); err != nil {
		return nil, err
	}
	index := -1
	rows := maps(current["opinions"])
	for i, row := range rows {
		if courtID(row["id"]) == opID {
			index = i
			op = row
			break
		}
	}
	if index < 0 {
		return nil, fail(404, "Opinion not found")
	}
	for k, v := range text {
		op[k] = v
	}
	rows[index] = op
	current["opinions"] = rows
	if _, err = tx.ExecContext(ctx, `UPDATE cc_judge_cache SET records=$2,metrics=NULL,updated_at=now() WHERE judge_id=$1`, id, jsonBytes(current)); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return op, nil
}
func (s *Server) judgeMetricsAPI(q request) (any, error) {
	id, err := legalPathID(q)
	if err != nil {
		return nil, err
	}
	force, err := judgeQueryBool(q, "force_recompute")
	if err != nil {
		return nil, err
	}
	ctx := q.r.Context()
	cache, err := s.judgeCache(ctx, id)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if !force {
		if when, e := time.Parse(time.RFC3339Nano, str(cache.Metrics, "computed_at")); e == nil && !when.After(now) && now.Sub(when) < 24*time.Hour {
			return cache.Metrics, nil
		}
	}
	result := judgeMetrics(id, cache.Profile, cache.Records, now)
	_, err = s.db.ExecContext(ctx, `UPDATE cc_judge_cache SET metrics=$2 WHERE judge_id=$1 AND generation=$3 AND updated_at=$4`, id, jsonBytes(result), cache.Generation, cache.Updated)
	return result, err
}
func (s *Server) judgeDelete(q request) (any, error) {
	if q.user.Role != "admin" {
		return nil, fail(403, "Only administrators can delete judge profiles")
	}
	id, err := legalPathID(q)
	if err != nil {
		return nil, err
	}
	ctx := q.r.Context()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,19))`, id); err != nil {
		return nil, err
	}
	// Match worker finalization's job-before-cache row lock order.
	if _, err = tx.ExecContext(ctx, `UPDATE cc_jobs SET status='cancelled',error='Judge profile deleted by an administrator',updated_at=now() WHERE kind='judge.build' AND input->>'judge_id'=$1 AND status IN ('pending','running')`, id); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM cc_judge_cache WHERE judge_id=$1`, id); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.audit(ctx, q.user.ID, "judge.delete", "judge", id, M{})
	return M{"status": "deleted", "message": "All data for judge " + id + " has been deleted"}, nil
}
