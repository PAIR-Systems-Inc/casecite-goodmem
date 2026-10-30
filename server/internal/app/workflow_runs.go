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

type workflowDB interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

const workflowRunColumns = `r.id,r.workflow_id,r.runner_id,COALESCE(r.job_id,''),r.status,COALESCE(r.error,''),r.data,r.created_at,r.completed_at`

func scanWorkflowRun(row scanner) (M, error) {
	var id, wid, runner, job, status, message string
	var b []byte
	var created time.Time
	var completed sql.NullTime
	if err := row.Scan(&id, &wid, &runner, &job, &status, &message, &b, &created, &completed); err != nil {
		return nil, err
	}
	run := M{}
	if err := json.Unmarshal(b, &run); err != nil {
		return nil, err
	}
	mergeMap(run, M{"id": id, "workflow_id": wid, "runner_id": runner, "job_id": nullable(job), "status": status, "error": nullable(message), "created_at": created.UTC().Format(time.RFC3339Nano), "completed_at": nil})
	if completed.Valid {
		run["completed_at"] = completed.Time.UTC().Format(time.RFC3339Nano)
	}
	return run, nil
}

func workflowStepRuns(ctx context.Context, db workflowDB, id string) ([]M, error) {
	rows, err := db.QueryContext(ctx, `SELECT step_index,status,data FROM cc_workflow_steps WHERE run_id=$1 ORDER BY step_index`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []M{}
	for rows.Next() {
		var i int
		var status string
		var b []byte
		if err = rows.Scan(&i, &status, &b); err != nil {
			return nil, err
		}
		step := M{}
		if err = json.Unmarshal(b, &step); err != nil {
			return nil, err
		}
		step["index"] = i
		step["status"] = status
		out = append(out, step)
	}
	return out, rows.Err()
}

func workflowPendingStep(index int, definition M) M {
	return M{"index": index, "name": definition["name"], "kind": definition["kind"], "status": "pending", "output": nil, "input_document_ids": []string{}, "produced_ids": M{}, "error": nil, "started_at": nil, "finished_at": nil}
}

func writeWorkflowStep(ctx context.Context, tx *sql.Tx, run string, step M) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO cc_workflow_steps(run_id,step_index,status,data) VALUES($1,$2,$3,$4) ON CONFLICT(run_id,step_index) DO UPDATE SET status=EXCLUDED.status,data=EXCLUDED.data`, run, number(step, "index", -1), str(step, "status"), jsonBytes(step))
	return err
}

func (s *Server) workflowRunResponse(ctx context.Context, user string, w, run M, detail bool) (M, error) {
	var completed int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM cc_workflow_steps WHERE run_id=$1 AND status IN ('completed','skipped_condition')`, str(run, "id")).Scan(&completed); err != nil {
		return nil, err
	}
	awaiting := object(run["awaiting"])
	approvers := []string{}
	var name any
	if str(run, "status") == "awaiting_approval" {
		approvers = stringsOf(awaiting["approver_ids"])
		name = awaiting["name"]
	}
	defs := maps(run["step_definitions"])
	ids := stringsOf(run["input_document_ids"])
	out := M{"id": run["id"], "workflow_id": run["workflow_id"], "workflow_name": w["name"], "workflow_version": number(run, "workflow_version", 1), "runner_id": run["runner_id"], "matter_id": run["matter_id"], "status": run["status"], "steps": len(defs), "completed_steps": completed, "documents": len(ids), "job_id": run["job_id"], "error": run["error"], "resumed_count": number(run, "resumed_count", 0), "awaiting_step": name, "approver_ids": approvers, "can_approve": str(run, "status") == "awaiting_approval" && slices.Contains(approvers, user), "created_at": run["created_at"], "completed_at": run["completed_at"]}
	if detail {
		steps, err := workflowStepRuns(ctx, s.db, str(run, "id"))
		if err != nil {
			return nil, err
		}
		refs := append(append([]string{}, ids...), workflowFixedDocuments(defs)...)
		for _, step := range steps {
			refs = append(refs, stringsOf(step["input_document_ids"])...)
		}
		out["step_definitions"] = defs
		out["step_runs"] = steps
		out["input_document_ids"] = ids
		out["document_refs"] = s.workflowDocumentRefs(ctx, user, refs)
	}
	return out, nil
}

func (s *Server) workflowRuns(q request) (any, error) {
	ctx, user, id, rid := q.r.Context(), q.user.ID, q.r.PathValue("id"), q.r.PathValue("run")
	var w M
	var err error
	if id != "" {
		w, err = s.getObject(ctx, user, "workflow", id)
		if err != nil {
			return nil, err
		}
	}
	if rid != "" {
		run, err := scanWorkflowRun(s.db.QueryRowContext(ctx, `SELECT `+workflowRunColumns+` FROM cc_workflow_runs r WHERE r.id=$1 AND r.workflow_id=$2`, rid, id))
		if err != nil {
			return nil, err
		}
		return s.workflowRunResponse(ctx, user, w, run, true)
	}
	limit := 200
	if id == "" {
		limit = 20
	}
	if raw := q.r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 200 {
			return nil, fail(422, "Limit must be between 1 and 200")
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+workflowRunColumns+`,o.data->>'name' FROM cc_workflow_runs r JOIN cc_objects o ON o.kind='workflow' AND o.id=r.workflow_id WHERE `+visibleSQL+` AND ($2='' OR r.workflow_id=$2) AND ($3='' OR o.matter_id=$3) ORDER BY r.created_at DESC LIMIT $4`, user, id, q.r.URL.Query().Get("matter_id"), limit)
	if err != nil {
		return nil, err
	}
	// Read all rows before issuing per-run detail queries: a small pool must not deadlock.
	items := []M{}
	for rows.Next() {
		var id, wid, runner, job, status, message, name string
		var b []byte
		var created time.Time
		var done sql.NullTime
		if err = rows.Scan(&id, &wid, &runner, &job, &status, &message, &b, &created, &done, &name); err != nil {
			rows.Close()
			return nil, err
		}
		r := M{}
		if err = json.Unmarshal(b, &r); err != nil {
			rows.Close()
			return nil, err
		}
		mergeMap(r, M{"id": id, "workflow_id": wid, "runner_id": runner, "job_id": nullable(job), "status": status, "error": nullable(message), "created_at": created.UTC().Format(time.RFC3339Nano), "completed_at": nil, "workflow_name": name})
		if done.Valid {
			r["completed_at"] = done.Time.UTC().Format(time.RFC3339Nano)
		}
		items = append(items, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []M{}
	for _, run := range items {
		summary, err := s.workflowRunResponse(ctx, user, M{"name": run["workflow_name"]}, run, false)
		if err != nil {
			return nil, err
		}
		out = append(out, summary)
	}
	return M{"runs": out}, nil
}

func (s *Server) workflowPreflight(ctx context.Context, user string, run M, start int) error {
	defs := maps(run["step_definitions"])
	ids := append(append([]string{}, stringsOf(run["input_document_ids"])...), workflowFixedDocuments(defs)...)
	if err := s.checkReviewDocuments(ctx, user, uniqueWorkflowStrings(ids)); err != nil {
		return err
	}
	for _, step := range defs[min(start, len(defs)):] {
		kind := str(step, "kind")
		if kind == "approval" {
			break
		}
		if kind == "citation_check" {
			continue
		}
		key, err := s.providerKey(ctx, user, "openai", s.cfg.LLMKey)
		if err != nil {
			return err
		}
		if key == "" {
			return bad("Configure an OpenAI-compatible API key in Settings before running model steps.")
		}
		break
	}
	return nil
}

func workflowQueueLock(ctx context.Context, tx *sql.Tx, user string) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,1))`, user); err != nil {
		return err
	}
	return nil
}

func queueWorkflow(ctx context.Context, tx *sql.Tx, user string, w, run M, start int) (M, error) {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM cc_jobs WHERE user_id=$1 AND status IN ('pending','running')`, user).Scan(&count); err != nil {
		return nil, err
	}
	if count >= 5 {
		return nil, fail(429, "Five jobs are already in progress. Wait for one to finish.")
	}
	job := uuid()
	input := M{"job_id": job, "workflow_id": w["id"], "run_id": run["id"], "start_index": start, "matter_id": run["matter_id"], "title": w["name"]}
	if _, err := tx.ExecContext(ctx, `INSERT INTO cc_jobs(id,user_id,kind,input) VALUES($1,$2,'workflow.run',$3)`, job, user, jsonBytes(input)); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE cc_workflow_runs SET job_id=$2,status='running',error=NULL,completed_at=NULL,data=$3 WHERE id=$1`, str(run, "id"), job, jsonBytes(run)); err != nil {
		return nil, err
	}
	return M{"run_id": run["id"], "job_id": job, "poll_url": "/api/v1/jobs/" + job, "steps": len(maps(run["step_definitions"])), "documents": len(stringsOf(run["input_document_ids"]))}, nil
}

func (s *Server) workflowStart(q request) (any, error) {
	ctx, user, id := q.r.Context(), q.user.ID, q.r.PathValue("id")
	in, err := body(q)
	if err != nil {
		return nil, err
	}
	ids, err := reviewStrings(in["document_ids"], 1000)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = workflowQueueLock(ctx, tx, user); err != nil {
		return nil, err
	}
	w, err := lockReviewObject(ctx, tx, user, "workflow", id, false)
	if err != nil {
		return nil, err
	}
	if w["archived_at"] != nil {
		return nil, bad("This workflow is archived. Unarchive it to run it.")
	}
	if len(ids) == 0 && str(w, "matter_id") != "" {
		rows, e := tx.QueryContext(ctx, `SELECT o.id FROM cc_objects o JOIN cc_matters m ON m.id=$2 WHERE o.kind='document' AND `+visibleSQL+` AND (o.matter_id=m.id OR (m.is_personal AND o.matter_id IS NULL AND o.owner_id=m.owner_id)) ORDER BY o.created_at,o.id`, user, str(w, "matter_id"))
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			var doc string
			if err = rows.Scan(&doc); err != nil {
				rows.Close()
				return nil, err
			}
			ids = append(ids, doc)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	if len(ids) > 1000 {
		return nil, bad("Select at most 1,000 documents for a workflow run")
	}
	if ids == nil {
		ids = []string{}
	}
	run := M{"id": uuid(), "workflow_id": id, "runner_id": user, "workflow_version": number(w, "version", 1), "step_definitions": w["steps"], "input_document_ids": ids, "matter_id": w["matter_id"], "resumed_count": 0, "awaiting": nil}
	if err = s.workflowPreflight(ctx, user, run, 0); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO cc_workflow_runs(id,workflow_id,runner_id,data) VALUES($1,$2,$3,$4)`, str(run, "id"), id, user, jsonBytes(run)); err != nil {
		return nil, err
	}
	for i, step := range maps(w["steps"]) {
		if err = writeWorkflowStep(ctx, tx, str(run, "id"), workflowPendingStep(i, step)); err != nil {
			return nil, err
		}
	}
	out, err := queueWorkflow(ctx, tx, user, w, run, 0)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.audit(ctx, user, "workflow_run_started", "workflow", id, M{"run_id": run["id"], "steps": out["steps"], "documents": out["documents"]})
	writeJSON(q.w, 202, out)
	return nil, nil
}

func (s *Server) workflowContinue(q request, action string, index int) (any, error) {
	ctx, user, id, rid := q.r.Context(), q.user.ID, q.r.PathValue("id"), q.r.PathValue("run")
	in := M{}
	var err error
	if q.r.ContentLength != 0 {
		in, err = body(q)
		if err != nil {
			return nil, err
		}
	}
	if note := in["note"]; note != nil {
		v := workflowValidator{}
		v.text(in, M{}, "note", "", 0, 2000, nil, false)
		if len(v.issues) > 0 {
			return nil, fail(422, "Decision notes must be at most 2,000 characters")
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = workflowQueueLock(ctx, tx, user); err != nil {
		return nil, err
	}
	w, err := lockReviewObject(ctx, tx, user, "workflow", id, false)
	if err != nil {
		return nil, err
	}
	run, err := scanWorkflowRun(tx.QueryRowContext(ctx, `SELECT `+workflowRunColumns+` FROM cc_workflow_runs r WHERE r.id=$1 AND r.workflow_id=$2 FOR UPDATE`, rid, id))
	if err != nil {
		return nil, err
	}
	if action != "reject" && w["archived_at"] != nil {
		return nil, bad("This workflow is archived. Unarchive it to run it.")
	}
	steps, err := workflowStepRuns(ctx, tx, rid)
	if err != nil {
		return nil, err
	}
	defs := maps(run["step_definitions"])
	status := str(run, "status")
	decision := action == "approve" || action == "reject"
	if decision {
		if status != "awaiting_approval" {
			return nil, bad("This run is not waiting for an approval")
		}
		awaiting := object(run["awaiting"])
		index = number(awaiting, "index", -1)
		if !slices.Contains(stringsOf(awaiting["approver_ids"]), user) {
			return nil, fail(403, "Only a named approver may decide this step")
		}
	} else {
		if (action == "retry" || action == "skip") && str(w, "user_id") != user && str(run, "runner_id") != user {
			return nil, fail(403, "Only the workflow owner or runner may retry or skip a step")
		}
		if action == "retry" {
			if status == "running" || status == "awaiting_approval" {
				return nil, bad("The run is in progress; wait for it to finish or cancel it")
			}
		} else if status != "failed" && status != "cancelled" {
			return nil, bad("Only a failed or cancelled run can be resumed or skip a step")
		}
		if action == "resume" {
			for i, step := range steps {
				if str(step, "status") == "skipped" && str(object(object(step["output"])["data"]), "skipped_by") != "" {
					continue
				}
				if slices.Contains([]string{"failed", "skipped", "pending", "running"}, str(step, "status")) {
					index = i
					break
				}
			}
		}
	}
	if index < 0 || index >= len(steps) {
		return nil, bad("No such step")
	}
	if action == "skip" && !slices.Contains([]string{"failed", "pending", "skipped"}, str(steps[index], "status")) {
		return nil, bad("Only a failed or not-yet-run step can be skipped")
	}
	start := index
	if decision || action == "skip" {
		start = index + 1
	}
	if action != "reject" {
		if err = s.workflowPreflight(ctx, user, run, start); err != nil {
			return nil, err
		}
	}
	if decision {
		step := steps[index]
		output := object(step["output"])
		data := object(output["data"])
		note := strings.TrimSpace(str(in, "note"))
		word := "Approved"
		step["status"] = "completed"
		if action == "reject" {
			word = "Rejected"
			step["status"] = "rejected"
		}
		mergeMap(data, M{"decision": strings.ToLower(word), "decided_by": user, "decided_by_email": q.user.Email, "decided_at": nowString(), "note": nullable(note)})
		output["data"] = data
		output["summary"] = fmt.Sprintf("%s by %s: %s", word, q.user.Email, str(data, "title"))
		step["output"] = output
		step["finished_at"] = nowString()
		if err = writeWorkflowStep(ctx, tx, rid, step); err != nil {
			return nil, err
		}
		run["decision_note"] = nullable(note)
		if action == "reject" {
			for _, later := range steps[index+1:] {
				if str(later, "status") == "pending" {
					later["status"] = "skipped"
					if err = writeWorkflowStep(ctx, tx, rid, later); err != nil {
						return nil, err
					}
				}
			}
			message := fmt.Sprintf("Rejected at step %d '%s' by %s", index+1, str(step, "name"), q.user.Email)
			if note != "" {
				message += ": " + note
			}
			if _, err = tx.ExecContext(ctx, `UPDATE cc_workflow_runs SET status='rejected',error=$2,completed_at=now(),data=$3 WHERE id=$1`, rid, charSlice(message, 0, 1000), jsonBytes(run)); err != nil {
				return nil, err
			}
			if err = tx.Commit(); err != nil {
				return nil, err
			}
			s.audit(ctx, user, "workflow_run_rejected", "workflow", id, M{"run_id": rid, "step_index": index, "note_chars": len([]rune(note))})
			return s.workflowRuns(q)
		}
	} else {
		for i := index; i < len(defs); i++ {
			if err = writeWorkflowStep(ctx, tx, rid, workflowPendingStep(i, defs[i])); err != nil {
				return nil, err
			}
		}
		run["resumed_count"] = number(run, "resumed_count", 0) + 1
		if action == "skip" {
			step := workflowPendingStep(index, defs[index])
			step["status"] = "skipped"
			step["finished_at"] = nowString()
			step["output"] = M{"summary": "Skipped by the user; later steps do not see this step's output.", "data": M{"skipped_by": user}, "document_ids": []string{}, "produced": M{}}
			if err = writeWorkflowStep(ctx, tx, rid, step); err != nil {
				return nil, err
			}
		}
	}
	run["awaiting"] = nil
	out, err := queueWorkflow(ctx, tx, user, w, run, start)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.audit(ctx, user, "workflow_run_"+action, "workflow", id, M{"run_id": rid, "step_index": index})
	writeJSON(q.w, 202, out)
	return nil, nil
}
