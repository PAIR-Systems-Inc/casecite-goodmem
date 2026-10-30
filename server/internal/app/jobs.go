package app

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
)

type jobOutputsKey struct{}
type jobLeaseKey struct{}
type jobLease struct{ ID, Token string }
type jobObject struct {
	User, Kind, ID, Matter string
	Data                   []byte
}
type jobAudit struct {
	User, Action, Kind, ID string
	Data                   M
}
type jobOutputs struct {
	sync.Mutex
	Objects []jobObject
	Audits  []jobAudit
	Judges  []jobJudge
}

// Stage durable outputs until the worker commits completion while still owning
// the job. A cancellation or lost lease must not leave a saved analysis behind.
func (s *Server) saveJobObject(ctx context.Context, user, kind, id, matter string, data M) error {
	if outputs, ok := ctx.Value(jobOutputsKey{}).(*jobOutputs); ok {
		outputs.Lock()
		defer outputs.Unlock()
		outputs.Objects = append(outputs.Objects, jobObject{user, kind, id, matter, jsonBytes(data)})
		return nil
	}
	return s.saveObject(ctx, user, kind, id, matter, data)
}
func (s *Server) jobAudit(ctx context.Context, user, action, kind, id string, data M) {
	if outputs, ok := ctx.Value(jobOutputsKey{}).(*jobOutputs); ok {
		outputs.Lock()
		defer outputs.Unlock()
		outputs.Audits = append(outputs.Audits, jobAudit{user, action, kind, id, data})
		return
	}
	s.audit(ctx, user, action, kind, id, data)
}

func (s *Server) createJob(q request) (any, error) {
	m, err := body(q)
	if err != nil {
		return nil, err
	}
	kind := str(m, "kind")
	switch kind {
	case "research", "analyze", "draft", "compare", "review":
	default:
		return nil, bad("Unknown job kind")
	}
	permission := "contracts.use"
	if kind == "research" {
		permission = "chat.use"
	}
	if err = permit(q, permission); err != nil {
		return nil, err
	}
	prompt := str(m, "prompt")
	if len(prompt) > 16000 {
		return nil, bad("Instructions exceed 16,000 characters")
	}
	if kind == "research" && prompt == "" {
		return nil, bad("Enter a research question")
	}
	if _, ok := m["document_ids"]; !ok {
		m["document_ids"] = []string{}
	} // Explicit scope; no accidental whole-library review.
	docs, err := s.scopedDocuments(q.r.Context(), q.user.ID, m)
	if err != nil {
		return nil, err
	}
	if kind != "research" && kind != "draft" && len(docs) == 0 {
		return nil, bad("Select indexed documents")
	}
	if kind == "compare" && len(docs) != 2 {
		return nil, bad("Select exactly two documents to compare")
	}
	if kind != "research" && len(docs) > 12 {
		return nil, bad("Select at most 12 documents")
	}
	if kind == "review" {
		fields := stringsOf(m["fields"])
		if len(fields) == 0 || len(fields) > 12 {
			return nil, bad("Provide 1–12 review fields")
		}
		for _, f := range fields {
			if len(f) > 300 {
				return nil, bad("Review fields must be under 300 characters")
			}
		}
	}
	if err = s.checkMatter(q.r.Context(), q.user.ID, str(m, "matter_id"), false); err != nil {
		return nil, err
	}
	if parent := str(m, "parent_id"); parent != "" {
		p, err := s.getObject(q.r.Context(), q.user.ID, "result", parent)
		if err != nil {
			return nil, err
		}
		if err = s.resultAccess(q.r.Context(), q.user.ID, p); err != nil {
			return nil, err
		}
		m["access_document_ids"] = append(stringsOf(p["document_ids"]), stringsOf(p["access_document_ids"])...)
	}
	tx, err := s.db.BeginTx(q.r.Context(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Per-user lock avoids a concurrent request burst bypassing the queue limit.
	if _, err = tx.ExecContext(q.r.Context(), `SELECT pg_advisory_xact_lock(hashtextextended($1,1))`, q.user.ID); err != nil {
		return nil, err
	}
	var count int
	if err = tx.QueryRowContext(q.r.Context(), `SELECT count(*) FROM cc_jobs WHERE user_id=$1 AND kind<>'document.ocr_index' AND status IN ('pending','running')`, q.user.ID).Scan(&count); err != nil {
		return nil, err
	}
	if count >= 5 {
		return nil, fail(429, "Five jobs are already queued. Wait or cancel a job.")
	}
	id := uuid()
	if _, err = tx.ExecContext(q.r.Context(), `INSERT INTO cc_jobs(id,user_id,kind,input) VALUES($1,$2,$3,$4)`, id, q.user.ID, kind, jsonBytes(m)); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.audit(q.r.Context(), q.user.ID, "job.create", "job", id, M{"kind": kind})
	writeJSON(q.w, 202, M{"job_id": id, "status": "pending"})
	return nil, nil
}
func (s *Server) getJob(q request) (any, error) {
	var id, kind, status string
	var input, result, progress []byte
	var errorText *string
	var created, updated time.Time
	err := s.db.QueryRowContext(q.r.Context(), `SELECT id,kind,status,input,result,error,created_at,updated_at,progress FROM cc_jobs WHERE id=$1 AND user_id=$2`, q.r.PathValue("id"), q.user.ID).Scan(&id, &kind, &status, &input, &result, &errorText, &created, &updated, &progress)
	if err != nil {
		return nil, err
	}
	in := M{}
	if err = json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	if err = s.resultAccess(q.r.Context(), q.user.ID, in); err != nil {
		return nil, err
	}
	var out any
	if len(result) > 0 {
		if err = json.Unmarshal(result, &out); err != nil {
			return nil, err
		}
	}
	var progressData any
	if err = json.Unmarshal(progress, &progressData); err != nil {
		return nil, err
	}
	return M{"job_id": id, "kind": kind, "status": status, "result": out, "error": errorText, "created_at": created, "updated_at": updated, "progress": progressData}, nil
}
func (s *Server) cancelJob(q request) (any, error) {
	r, err := s.db.ExecContext(q.r.Context(), `UPDATE cc_jobs SET status='cancelled',error='Cancelled by the user',updated_at=now() WHERE id=$1 AND user_id=$2 AND status IN ('pending','running')`, q.r.PathValue("id"), q.user.ID)
	if err != nil {
		return nil, err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return nil, fail(404, "Job not found or cannot be cancelled")
	}
	return M{"status": "cancelled", "job_id": q.r.PathValue("id")}, nil
}
func (s *Server) listJobs(q request) (any, error) {
	limit := 20
	if raw := q.r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			return nil, bad("limit must be between 1 and 100")
		}
		limit = n
	}
	rows, err := s.db.QueryContext(q.r.Context(), `SELECT id,kind,status,created_at FROM cc_jobs WHERE user_id=$1 ORDER BY created_at DESC LIMIT $2`, q.user.ID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []M{}
	for rows.Next() {
		var id, kind, status string
		var created time.Time
		if err = rows.Scan(&id, &kind, &status, &created); err != nil {
			return nil, err
		}
		out = append(out, M{"job_id": id, "kind": kind, "status": status, "created_at": created})
	}
	return M{"jobs": out, "count": len(out)}, rows.Err()
}

func (s *Server) StartWorkers() {
	s.wg.Add(1)
	go s.emailWorker()
	for i := 0; i < 2; i++ {
		s.wg.Add(1)
		go s.worker()
	}
	s.wg.Add(1)
	go s.maintenance()
}
func (s *Server) worker() {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
		lease := token()
		var id, user, kind string
		var b []byte
		err := s.db.QueryRowContext(s.ctx, `UPDATE cc_jobs SET status='running',lease_token=$1,lease_until=now()+interval '30 seconds',updated_at=now() WHERE id=(SELECT id FROM cc_jobs WHERE status='pending' ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id,user_id,kind,input`, lease).Scan(&id, &user, &kind, &b)
		if notFound(err) {
			continue
		}
		if err != nil {
			if s.ctx.Err() == nil {
				s.log.Error("job claim failed", "error", err)
			}
			continue
		}
		s.runJob(id, user, kind, lease, b)
	}
}
func (s *Server) runJob(id, user, kind, lease string, b []byte) {
	limit := s.cfg.JobTimeout
	if limit <= 0 {
		limit = time.Hour
	}
	ctx, cancel := context.WithTimeout(s.ctx, limit)
	defer cancel()
	outputs := &jobOutputs{}
	ctx = context.WithValue(ctx, jobOutputsKey{}, outputs)
	ctx = context.WithValue(ctx, jobLeaseKey{}, jobLease{id, lease})
	done := make(chan struct{})
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-t.C:
				r, err := s.db.ExecContext(ctx, `UPDATE cc_jobs SET lease_until=now()+interval '30 seconds',updated_at=now() WHERE id=$1 AND lease_token=$2 AND status='running'`, id, lease)
				if err != nil {
					cancel()
					return
				}
				n, _ := r.RowsAffected()
				if n == 0 {
					cancel()
					return
				}
			}
		}
	}()
	input := M{}
	err := json.Unmarshal(b, &input)
	var result M
	if err == nil {
		result, err = s.executeSafely(ctx, user, kind, input)
	}
	close(done)
	<-heartbeatDone
	finish, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	if err == nil {
		err = s.resultAccess(finish, user, input)
	}
	status := "completed"
	message := ""
	errorStatus := 0
	if err != nil {
		status = "failed"
		message = "The job failed. Check the server log and retry."
		var ae *apiError
		if errors.As(err, &ae) {
			message = ae.message
			errorStatus = ae.status
		}
		if errors.Is(err, context.DeadlineExceeded) {
			message = "The job exceeded its configured time limit (" + limit.String() + "). Retry with a narrower request or ask the administrator to increase JOB_TIMEOUT_SECONDS."
		}
		if errors.Is(err, context.Canceled) {
			message = "The worker stopped or the job was cancelled. Retry explicitly to avoid duplicate model charges."
		}
		s.log.Warn("job failed", "id", id, "kind", kind, "error", err)
	}
	tx, txErr := s.db.BeginTx(finish, nil)
	if txErr != nil {
		s.log.Error("job finalization failed", "error", txErr)
		return
	}
	defer tx.Rollback()
	r, txErr := tx.ExecContext(finish, `UPDATE cc_jobs SET status=$3,result=$4,error=$5,lease_until=NULL,updated_at=now(),progress=CASE WHEN $6::int>0 THEN progress || jsonb_build_object('error_status',$6::int) ELSE progress END WHERE id=$1 AND lease_token=$2 AND status='running'`, id, lease, status, jsonBytes(result), nullable(message), errorStatus)
	if txErr != nil {
		s.log.Error("job finalization failed", "error", txErr)
		return
	}
	n, _ := r.RowsAffected()
	if n == 1 && err == nil && kind != "document.ocr_index" {
		for _, output := range outputs.Judges {
			r, saveErr := tx.ExecContext(finish, `UPDATE cc_judge_cache SET profile=$3,records=$4,metrics=NULL,updated_at=now() WHERE judge_id=$1 AND generation=$2 AND build_job_id=$5`, output.ID, output.Generation, jsonBytes(output.Profile), jsonBytes(output.Records), id)
			if saveErr != nil {
				s.log.Error("judge profile save failed", "error", saveErr)
				return
			}
			if affected, _ := r.RowsAffected(); affected != 1 {
				s.log.Warn("judge build reservation changed", "job", id)
				return
			}
		}
		for _, output := range outputs.Objects {
			_, txErr = tx.ExecContext(finish, `INSERT INTO cc_objects(kind,id,owner_id,matter_id,data) VALUES($1,$2,$3,$4,$5)`, output.Kind, output.ID, output.User, nullable(output.Matter), output.Data)
			if txErr != nil {
				s.log.Error("job output save failed", "error", txErr)
				return
			}
		}
		if str(result, "kind") == "" {
			result["kind"] = kind
		}
		if str(result, "title") == "" {
			result["title"] = shortTitle(fallback(str(input, "prompt"), str(input, "message")), kind)
		}
		result["document_ids"] = input["document_ids"]
		result["access_document_ids"] = input["access_document_ids"]
		result["prompt"] = input["prompt"]
		result["parent_id"] = input["parent_id"]
		_, txErr = tx.ExecContext(finish, `INSERT INTO cc_objects(kind,id,owner_id,matter_id,data) VALUES('result',$1,$2,$3,$4)`, id, user, nullable(str(input, "matter_id")), jsonBytes(result))
		if txErr != nil {
			s.log.Error("result save failed", "error", txErr)
			return
		}
	}
	if txErr = tx.Commit(); txErr != nil {
		s.log.Error("job commit failed", "error", txErr)
		return
	}
	if n == 1 && err == nil {
		for _, event := range outputs.Audits {
			s.audit(finish, event.User, event.Action, event.Kind, event.ID, event.Data)
		}
	}
}
func (s *Server) executeSafely(ctx context.Context, user, kind string, input M) (out M, err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("job processor panic")
		}
	}()
	return s.execute(ctx, user, kind, input)
}
func (s *Server) maintenance() {
	defer s.wg.Done()
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			s.cleanupDocumentOCR(s.ctx)
			_, err := s.db.ExecContext(s.ctx, `UPDATE cc_jobs SET status='failed',error='Worker lease expired. Retry explicitly; the previous request may have incurred a model charge.',updated_at=now() WHERE status='running' AND lease_until<now()`)
			if err != nil {
				s.log.Error("job recovery failed", "error", err)
			}
			for _, query := range []string{`DELETE FROM cc_kv WHERE expires_at<now()`, `DELETE FROM cc_sessions WHERE expires_at<now()`, `DELETE FROM cc_refresh_used WHERE expires_at<now()`, `DELETE FROM cc_device_codes WHERE expires_at<now()`, `DELETE FROM cc_outbox WHERE created_at<now()-interval '30 days' AND status IN ('sent','failed')`, `DELETE FROM cc_jobs WHERE created_at<now()-interval '30 days' AND status NOT IN ('pending','running')`} {
				if _, err = s.db.ExecContext(s.ctx, query); err != nil {
					s.log.Warn("expired state cleanup failed", "error", err)
				}
			}
		}
	}
}
func shortTitle(prompt, fallback string) string {
	r := []rune(strings.TrimSpace(prompt))
	if len(r) > 90 {
		return string(r[:90]) + "…"
	}
	if len(r) == 0 {
		return fallback
	}
	return string(r)
}
func (s *Server) resultAccess(ctx context.Context, user string, m M) error {
	for _, id := range append(stringsOf(m["document_ids"]), stringsOf(m["access_document_ids"])...) {
		if _, err := s.getObject(ctx, user, "document", id); err != nil {
			if notFound(err) {
				return fail(403, "A source document is deleted or no longer accessible")
			}
			return err
		}
	}
	return nil
}
func (s *Server) listResults(q request) (any, error) {
	a, err := s.objects(q.r.Context(), q.user.ID, "result")
	if err != nil {
		return nil, err
	}
	out := []M{}
	for _, m := range a {
		if err = s.resultAccess(q.r.Context(), q.user.ID, m); err != nil {
			continue
		}
		out = append(out, M{"id": m["id"], "kind": m["kind"], "title": m["title"], "created_at": m["created_at"], "matter_id": m["matter_id"], "user_id": m["user_id"]})
	}
	return M{"results": out}, nil
}
func (s *Server) getResult(q request) (any, error) {
	m, err := s.getObject(q.r.Context(), q.user.ID, "result", q.r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	if err = s.resultAccess(q.r.Context(), q.user.ID, m); err != nil {
		return nil, err
	}
	return m, nil
}
func (s *Server) deleteResult(q request) (any, error) {
	m, err := s.getObject(q.r.Context(), q.user.ID, "result", q.r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	if err = own(q, m); err != nil {
		return nil, err
	}
	_, err = s.db.ExecContext(q.r.Context(), `DELETE FROM cc_objects WHERE kind='result' AND id=$1`, q.r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	_, err = s.db.ExecContext(q.r.Context(), `DELETE FROM cc_jobs WHERE id=$1 AND user_id=$2 AND status NOT IN ('pending','running')`, q.r.PathValue("id"), q.user.ID)
	return M{"status": "deleted"}, err
}
