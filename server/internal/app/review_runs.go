package app

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"
)

func (s *Server) reviewTableEstimate(q request) (any, error) {
	table, err := s.getObject(q.r.Context(), q.user.ID, "review_table", q.r.PathValue("table_id"))
	if err != nil {
		return nil, err
	}
	fields, documents, tokens := len(maps(table["fields"])), 0, 0
	for _, id := range stringsOf(table["document_ids"]) {
		doc, err := s.getObject(q.r.Context(), q.user.ID, "document", id)
		if err != nil {
			if notFound(err) {
				continue
			}
			return nil, err
		}
		documents++
		// GoodMem does not expose chunk counts. Use measured source text length
		// when available; otherwise the excerpt ceiling is an honest upper
		// budget, including for compressed/scanned documents.
		chars := number(doc, "review_text_length", reviewExcerptBudget)
		tokens += reviewEstimateTokens(chars, fields)
	}
	model, err := s.utilityModelName(q.r.Context(), q.user.ID)
	return M{"documents": documents, "fields": fields, "cells": documents * fields, "llm_calls": documents, "approx_input_tokens": tokens, "model": model}, err
}

func (s *Server) startReviewRun(q request, id string, in M) (M, error) {
	ctx, user := q.r.Context(), q.user.ID
	docs, err := reviewStrings(in["document_ids"], 1000)
	if err != nil {
		return nil, err
	}
	keys, err := reviewStrings(in["field_keys"], 40)
	if err != nil {
		return nil, err
	}
	if err = s.requireResearchModel(ctx, user); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,1))`, user); err != nil {
		return nil, err
	}
	table, err := lockReviewObject(ctx, tx, user, "review_table", id, false)
	if err != nil {
		return nil, err
	}
	allDocs, allFields := stringsOf(table["document_ids"]), maps(table["fields"])
	if len(allDocs) == 0 || len(allFields) == 0 {
		return nil, bad("Add at least one document and one field before running.")
	}
	selectedDocs, selectedFields := []string{}, []M{}
	for _, doc := range allDocs {
		if len(docs) == 0 || slices.Contains(docs, doc) {
			selectedDocs = append(selectedDocs, doc)
		}
	}
	for _, f := range allFields {
		if len(keys) == 0 || slices.Contains(keys, str(f, "key")) {
			selectedFields = append(selectedFields, f)
		}
	}
	if len(selectedDocs) == 0 {
		return nil, bad("None of the given documents is in this table.")
	}
	if len(selectedFields) == 0 {
		return nil, bad("None of the given fields is in this table.")
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM cc_jobs WHERE user_id=$1 AND kind<>'document.ocr_index' AND status IN ('pending','running')`, user).Scan(&count); err != nil {
		return nil, err
	}
	if count >= 5 {
		return nil, fail(429, "Five jobs are already in progress. Wait for one to finish.")
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM cc_review_runs r JOIN cc_jobs j ON j.id=r.job_id WHERE r.table_id=$1 AND j.status IN ('pending','running')`, id).Scan(&count); err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, fail(409, "A run is already in progress for this table. Wait for it to finish or cancel it before rerunning.")
	}
	scope := M{"document_ids": nil, "field_keys": nil}
	if len(docs) > 0 {
		scope["document_ids"] = selectedDocs
	}
	if len(keys) > 0 {
		selectedKeys := []string{}
		for _, f := range selectedFields {
			selectedKeys = append(selectedKeys, str(f, "key"))
		}
		scope["field_keys"] = selectedKeys
	}
	job, run := uuid(), uuid()
	input := M{"job_id": job, "run_id": run, "table_id": id, "review_document_ids": selectedDocs, "fields": selectedFields, "matter_id": table["matter_id"], "title": table["name"]}
	if _, err = tx.ExecContext(ctx, `INSERT INTO cc_jobs(id,user_id,kind,input) VALUES($1,$2,'review_table.run',$3)`, job, user, jsonBytes(input)); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO cc_review_runs(id,table_id,runner_id,job_id,scope) VALUES($1,$2,$3,$4,$5)`, run, id, user, job, jsonBytes(scope)); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.audit(ctx, user, "review_table.run.started", "review_table", id, M{"run_id": run, "documents": len(selectedDocs), "fields": len(selectedFields)})
	return M{"job_id": job, "poll_url": "/api/v1/jobs/" + job, "run_id": run, "documents": len(selectedDocs), "fields": len(selectedFields)}, nil
}
func (s *Server) reviewTableRun(q request) (any, error) {
	in := M{}
	if q.r.ContentLength != 0 {
		var err error
		in, err = body(q)
		if err != nil {
			return nil, err
		}
	}
	started, err := s.startReviewRun(q, q.r.PathValue("table_id"), in)
	if err != nil {
		return nil, err
	}
	writeJSON(q.w, 202, started)
	return nil, nil
}
func (s *Server) reviewTableRuns(q request) (any, error) {
	id := q.r.PathValue("table_id")
	if _, err := s.getObject(q.r.Context(), q.user.ID, "review_table", id); err != nil {
		return nil, err
	}
	limit, err := sessionLimit(q, 50)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(q.r.Context(), `SELECT id,runner_id,job_id,scope,status,counts,error,started_at,finished_at FROM cc_review_runs WHERE table_id=$1 ORDER BY started_at DESC LIMIT $2`, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []M{}
	for rows.Next() {
		var run, runner, status string
		var job, message *string
		var scope, counts []byte
		var start time.Time
		var finish *time.Time
		if err = rows.Scan(&run, &runner, &job, &scope, &status, &counts, &message, &start, &finish); err != nil {
			return nil, err
		}
		var scopeData, countsData M
		if err = json.Unmarshal(scope, &scopeData); err != nil {
			return nil, err
		}
		if len(counts) > 0 {
			if err = json.Unmarshal(counts, &countsData); err != nil {
				return nil, err
			}
		}
		runs = append(runs, M{"id": run, "table_id": id, "runner_id": runner, "job_id": job, "scope": scopeData, "status": status, "counts": countsData, "error": message, "started_at": start, "finished_at": finish})
	}
	return M{"runs": runs}, rows.Err()
}

func (s *Server) writeReviewResults(ctx context.Context, user string, input M, doc string, fields []M, results map[string]M, textLength int) (M, error) {
	account, err := s.loadUser(ctx, user)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(account.Permissions, "contracts.use") {
		return nil, fail(403, "Contract access is no longer available")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	lease, ok := ctx.Value(jobLeaseKey{}).(jobLease)
	if !ok {
		return nil, fmt.Errorf("review writes require an active job lease")
	}
	var job string
	// Lock queue first, then table. Cancellation must precede or follow the
	// entire document write, and a worker with a lost lease may not commit.
	if err = tx.QueryRowContext(ctx, `SELECT id FROM cc_jobs WHERE id=$1 AND lease_token=$2 AND status='running' AND lease_until>now() FOR SHARE`, lease.ID, lease.Token).Scan(&job); err != nil {
		return nil, context.Canceled
	}
	table, err := lockReviewObject(ctx, tx, user, "review_table", str(input, "table_id"), false)
	if err != nil {
		return nil, err
	}
	count := M{"extracted": 0, "unverified": 0, "not_found": 0, "errors": 0, "changed": 0, "skipped": 0}
	accessible := true
	if _, err = s.getObject(ctx, user, "document", doc); err != nil {
		if !notFound(err) {
			return nil, err
		}
		accessible = false
	}
	for _, f := range fields {
		key := str(f, "key")
		current := M{}
		for _, candidate := range maps(table["fields"]) {
			if str(candidate, "key") == key {
				current = candidate
				break
			}
		}
		// A removed row/column or a changed extraction definition makes the
		// queued result stale. Never resurrect it after an owner edit.
		if !reviewInScope(table, doc, key) || string(jsonBytes(current)) != string(jsonBytes(f)) {
			count["skipped"] = number(count, "skipped", 0) + 1
			continue
		}
		result := results[key]
		if !accessible {
			result = reviewErrorCell("Document is not accessible or no longer exists")
		}
		cell, existing, err := loadReviewCell(ctx, tx, str(table, "id"), doc, key)
		if err != nil {
			return nil, err
		}
		changed := applyReviewModel(cell, result, s.cfg.LLMUtilityModel, textLength, user)
		if existing && changed {
			count["changed"] = number(count, "changed", 0) + 1
		}
		if err = saveReviewCell(ctx, tx, str(table, "id"), cell); err != nil {
			return nil, err
		}
		status := str(result, "status")
		if status == "error" {
			status = "errors"
		}
		count[status] = number(count, status, 0) + 1
	}
	return count, tx.Commit()
}

func (s *Server) executeReviewTable(parent context.Context, user string, input M) (M, error) {
	account, err := s.loadUser(parent, user)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(account.Permissions, "contracts.use") {
		return nil, fail(403, "Contract access is no longer available")
	}
	if _, err = s.getObject(parent, user, "review_table", str(input, "table_id")); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	docs, fields := stringsOf(input["review_document_ids"]), maps(input["fields"])
	tally := M{"table_id": input["table_id"], "documents": len(docs), "fields": len(fields), "cells": len(docs) * len(fields), "extracted": 0, "unverified": 0, "not_found": 0, "errors": 0, "changed": 0, "skipped": 0}
	s.jobProgress(ctx, str(input, "job_id"), fmt.Sprintf("Extracting 0/%d documents", len(docs)), 0)
	queue := make(chan string, len(docs))
	for _, doc := range docs {
		queue <- doc
	}
	close(queue)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	done := 0
	concurrency := s.cfg.ReviewConcurrency
	if concurrency <= 0 {
		concurrency = 4
	}
	for i := 0; i < min(concurrency, len(docs)); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for doc := range queue {
				if ctx.Err() != nil {
					return
				}
				results, length := s.extractReviewDocument(ctx, user, doc, fields)
				counts, err := s.writeReviewResults(ctx, user, input, doc, fields, results, length)
				mu.Lock()
				if err != nil {
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					cancel()
					return
				}
				for key, value := range counts {
					tally[key] = number(tally, key, 0) + value.(int)
				}
				done++
				s.jobProgress(ctx, str(input, "job_id"), fmt.Sprintf("Extracted %d/%d documents", done, len(docs)), float64(done)/float64(len(docs)))
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	s.jobAudit(ctx, user, "review_table.run.completed", "review_table", str(input, "table_id"), tally)
	return tally, nil
}
