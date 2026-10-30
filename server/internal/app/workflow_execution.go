package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

type workflowProducts struct {
	objects *jobOutputs
	cells   map[string][]M
}

func (s *Server) workflowWrite(ctx context.Context, user string, input M) (*sql.Tx, M, M, error) {
	account, err := s.loadUser(ctx, user)
	if err != nil {
		return nil, nil, nil, err
	}
	if !slices.Contains(account.Permissions, "contracts.use") {
		return nil, nil, nil, fail(403, "Workflow access is no longer available")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	failure := func(e error) (*sql.Tx, M, M, error) { tx.Rollback(); return nil, nil, nil, e }
	lease, ok := ctx.Value(jobLeaseKey{}).(jobLease)
	if !ok {
		return failure(errors.New("workflow writes require an active job lease"))
	}
	var id string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM cc_jobs WHERE id=$1 AND lease_token=$2 AND status='running' AND lease_until>now() FOR SHARE`, lease.ID, lease.Token).Scan(&id); err != nil {
		return failure(context.Canceled)
	}
	w, err := lockReviewObject(ctx, tx, user, "workflow", str(input, "workflow_id"), false)
	if err != nil {
		return failure(err)
	}
	run, err := scanWorkflowRun(tx.QueryRowContext(ctx, `SELECT `+workflowRunColumns+` FROM cc_workflow_runs r WHERE r.id=$1 AND r.workflow_id=$2 AND r.job_id=$3 AND r.status='running' FOR UPDATE`, str(input, "run_id"), str(input, "workflow_id"), lease.ID))
	if err != nil {
		return failure(err)
	}
	return tx, w, run, nil
}

func (s *Server) completeWorkflowStep(ctx context.Context, user string, input M, index int, status string, output M, products *workflowProducts) error {
	tx, _, run, err := s.workflowWrite(ctx, user, input)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	steps, err := workflowStepRuns(ctx, tx, str(run, "id"))
	if err != nil {
		return err
	}
	if index < 0 || index >= len(steps) || str(steps[index], "status") != "running" {
		return fail(409, "The workflow step changed while it was running")
	}
	if products != nil {
		for _, p := range products.objects.Objects {
			data := M{}
			if err = json.Unmarshal(p.Data, &data); err != nil {
				return err
			}
			if err = s.resultAccess(ctx, user, data); err != nil {
				return err
			}
			if err = s.checkMatter(ctx, user, p.Matter, false); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO cc_objects(kind,id,owner_id,matter_id,data) VALUES($1,$2,$3,$4,$5)`, p.Kind, p.ID, p.User, nullable(p.Matter), p.Data); err != nil {
				return err
			}
		}
		for table, cells := range products.cells {
			for _, cell := range cells {
				if err = saveReviewCell(ctx, tx, table, cell); err != nil {
					return err
				}
			}
		}
	}
	step := steps[index]
	mergeMap(step, M{"status": status, "output": output, "input_document_ids": output["document_ids"], "produced_ids": output["produced"], "error": nil, "finished_at": nowString()})
	if err = writeWorkflowStep(ctx, tx, str(run, "id"), step); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if products != nil {
		for _, event := range products.objects.Audits {
			s.audit(ctx, event.User, event.Action, event.Kind, event.ID, event.Data)
		}
	}
	s.audit(ctx, user, "workflow_step_completed", "workflow", str(input, "workflow_id"), M{"run_id": input["run_id"], "step_index": index, "status": status})
	return nil
}

func (s *Server) executeWorkflow(ctx context.Context, user string, input M) (M, error) {
	tx, w, run, err := s.workflowWrite(ctx, user, input)
	if err != nil {
		return nil, err
	}
	rows, err := workflowStepRuns(ctx, tx, str(run, "id"))
	tx.Rollback()
	if err != nil {
		return nil, err
	}
	outputs, produced := M{}, M{}
	done := 0
	for _, row := range rows {
		if slices.Contains([]string{"completed", "skipped_condition"}, str(row, "status")) {
			outputs[str(row, "name")] = row["output"]
			done++
			for key, ids := range object(row["produced_ids"]) {
				produced[key] = number(produced, key, 0) + len(stringsOf(ids))
			}
		}
	}
	defs := maps(run["step_definitions"])
	result := func(status string) M {
		return M{"run_id": run["id"], "status": status, "steps": len(defs), "completed_steps": done, "documents": len(stringsOf(run["input_document_ids"])), "produced": produced}
	}
	for index := number(input, "start_index", 0); index < len(defs); index++ {
		step := defs[index]
		name := str(step, "name")
		tx, w, _, err = s.workflowWrite(ctx, user, input)
		if err != nil {
			return nil, err
		}
		current := workflowPendingStep(index, step)
		current["status"] = "running"
		current["started_at"] = nowString()
		var status string
		if err = tx.QueryRowContext(ctx, `SELECT status FROM cc_workflow_steps WHERE run_id=$1 AND step_index=$2`, str(run, "id"), index).Scan(&status); err != nil {
			tx.Rollback()
			return nil, err
		}
		if status != "pending" {
			tx.Rollback()
			return nil, fail(409, "This workflow step is no longer pending")
		}
		if err = writeWorkflowStep(ctx, tx, str(run, "id"), current); err != nil {
			tx.Rollback()
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		s.jobProgress(ctx, str(input, "job_id"), fmt.Sprintf("Step %d/%d (%s): starting", index+1, len(defs), name), float64(index)/float64(len(defs)))
		if condition := step["condition"]; condition != nil {
			verdict := evaluateWorkflowCondition(object(condition), outputs)
			if verdict["result"] != true {
				output := M{"summary": "Skipped: condition not met.", "data": M{"condition": verdict}, "document_ids": []string{}, "produced": M{}}
				if err = s.completeWorkflowStep(ctx, user, input, index, "skipped_condition", output, nil); err != nil {
					return nil, err
				}
				outputs[name] = output
				done++
				continue
			}
		}
		if str(step, "kind") == "approval" {
			tx, w, currentRun, e := s.workflowWrite(ctx, user, input)
			if e != nil {
				return nil, e
			}
			p := object(step["params"])
			approvers, e := s.workflowApprovers(ctx, tx, w, p["approvers"])
			if e != nil {
				tx.Rollback()
				return nil, e
			}
			data := M{"title": p["title"], "instructions": p["instructions"], "approvers": p["approvers"], "approver_ids": approvers}
			current["status"] = "awaiting"
			current["output"] = M{"summary": "Waiting for approval: " + str(p, "title"), "data": data, "document_ids": []string{}, "produced": M{}}
			if e = writeWorkflowStep(ctx, tx, str(run, "id"), current); e != nil {
				tx.Rollback()
				return nil, e
			}
			currentRun["awaiting"] = M{"index": index, "name": name, "title": p["title"], "approver_ids": approvers}
			if _, e = tx.ExecContext(ctx, `UPDATE cc_workflow_runs SET status='awaiting_approval',job_id=NULL,error=NULL,data=$2 WHERE id=$1`, str(run, "id"), jsonBytes(currentRun)); e != nil {
				tx.Rollback()
				return nil, e
			}
			if e = tx.Commit(); e != nil {
				return nil, e
			}
			s.audit(ctx, user, "workflow_run_awaiting_approval", "workflow", str(w, "id"), M{"run_id": run["id"], "step_index": index})
			out := result("awaiting_approval")
			out["awaiting_step"] = name
			return out, nil
		}
		products := &workflowProducts{objects: &jobOutputs{}, cells: map[string][]M{}}
		stepCtx := context.WithValue(ctx, jobOutputsKey{}, products.objects)
		output, e := s.executeWorkflowStep(stepCtx, user, w, run, step, outputs, products)
		if e == nil {
			output, e = capWorkflowOutput(output, number(s.workflowCaps(), "max_output_bytes", 524288))
		}
		if e != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			message := "The operation failed. Check the server log and retry."
			var ae *apiError
			if errors.As(e, &ae) {
				message = ae.message
			}
			s.log.Warn("workflow step failed", "run", str(run, "id"), "step", name, "error", e)
			// The queue's finalizer atomically marks this step failed and later
			// steps skipped, including cancellation, panic and lease-expiry paths.
			return nil, bad(fmt.Sprintf("Step %d/%d '%s' failed: %s", index+1, len(defs), name, message))
		}
		if err = s.completeWorkflowStep(ctx, user, input, index, "completed", output, products); err != nil {
			return nil, err
		}
		outputs[name] = output
		done++
		for key, ids := range object(output["produced"]) {
			produced[key] = number(produced, key, 0) + len(stringsOf(ids))
		}
	}
	s.jobProgress(ctx, str(input, "job_id"), "Workflow complete", 1)
	return result("completed"), nil
}
