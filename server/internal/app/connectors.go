package app

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
)

func (s *Server) connectorRoutes() {
	s.api("GET /api/v1/connectors", false, s.connectorStatuses)
	s.api("GET /api/v1/connectors/{provider}/status", false, s.connectorStatusRoute)
	s.api("GET /api/v1/connectors/{provider}/auth", false, s.beginConnectorAuth)
	s.api("GET /api/v1/connectors/{provider}/authorize", true, s.launchConnectorAuth)
	s.api("GET /api/v1/connectors/{provider}/callback", true, s.connectorCallback)
	s.api("POST /api/v1/connectors/{provider}/disconnect", false, s.disconnectConnector)
	s.api("POST /api/v1/connectors/{provider}/sync", false, s.startConnectorSync)
	s.api("GET /api/v1/connectors/{provider}/sync/{sync_id}", false, s.connectorSyncStatus)
	s.api("GET /api/v1/admin/integrations/connectors", false, s.connectorIntegrations)
	s.api("POST /api/v1/admin/integrations/connectors/{provider}", false, s.connectorIntegrations)
	s.api("DELETE /api/v1/admin/integrations/connectors/{provider}", false, s.connectorIntegrations)
}
func (s *Server) connectorStatus(ctx context.Context, user User, d connectorDefinition) (M, error) {
	v, err := s.connectorConfig(ctx, d.Settings)
	if err != nil {
		return nil, err
	}
	out := M{"id": d.ID, "type": d.ID, "connected": false, "configured": s.connectorConfigured(d, v), "experimental": d.Experimental, "admin_only": d.AdminOnly, "account_name": nil, "account_email": nil, "docs_indexed": 0, "last_sync": nil}
	if !s.connectorConfigured(d, v) || d.AdminOnly && !slices.Contains(user.Permissions, "admin.settings") {
		return out, nil
	}
	account, err := s.connectorCredentials(ctx, user.ID, d, v, "")
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return out, nil
	}
	info, err := s.connectorAccountInfo(ctx, d, v, account.Credentials)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		out["error"] = "Failed to get account info"
		return out, nil
	}
	var count int
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM cc_objects WHERE owner_id=$1 AND kind='document' AND data->>'source'=$2 AND data->>'status'='indexed'`, user.ID, d.ID).Scan(&count); err != nil {
		return nil, err
	}
	out["connected"], out["account_name"], out["account_email"], out["docs_indexed"], out["last_sync"] = true, info["name"], info["email"], count, account.LastSync
	return out, nil
}
func (s *Server) connectorStatuses(q request) (any, error) {
	results := make([]M, len(connectorDefinitions))
	errs := make([]error, len(connectorDefinitions))
	gate := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, d := range connectorDefinitions {
		if d.Experimental && !s.cfg.ExperimentalConnectors {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case gate <- struct{}{}:
				defer func() { <-gate }()
			case <-q.r.Context().Done():
				errs[i] = q.r.Context().Err()
				return
			}
			results[i], errs[i] = s.connectorStatus(q.r.Context(), q.user, d)
		}()
	}
	wg.Wait()
	out := []M{}
	for i, result := range results {
		if errs[i] != nil {
			return nil, errs[i]
		}
		if result != nil {
			out = append(out, result)
		}
	}
	return M{"connectors": out}, nil
}
func (s *Server) connectorStatusRoute(q request) (any, error) {
	d, err := s.connectorDefinition(q.r.PathValue("provider"))
	if err != nil {
		return nil, err
	}
	return s.connectorStatus(q.r.Context(), q.user, d)
}

func (s *Server) disconnectConnector(q request) (any, error) {
	d, err := s.connectorDefinition(q.r.PathValue("provider"))
	if err != nil {
		return nil, err
	}
	if err = connectorPermit(q.user, d); err != nil {
		return nil, err
	}
	ctx := q.r.Context()
	v, err := s.connectorConfig(ctx, d.Settings)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Jobs before account matches completion/publication lock order.
	if _, err = tx.ExecContext(ctx, `UPDATE cc_jobs SET status='cancelled',error='Connector disconnected',updated_at=now() WHERE user_id=$1 AND kind='connector.sync' AND input->>'provider'=$2 AND status IN ('pending','running')`, q.user.ID, d.ID); err != nil {
		return nil, err
	}
	var encrypted string
	err = tx.QueryRowContext(ctx, `SELECT credentials FROM cc_connector_accounts WHERE user_id=$1 AND provider=$2 FOR UPDATE`, q.user.ID, d.ID).Scan(&encrypted)
	if err != nil && !notFound(err) {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE cc_connector_accounts SET generation=$3,credentials='',account='{}',last_sync=NULL,updated_at=now() WHERE user_id=$1 AND provider=$2`, q.user.ID, d.ID, uuid()); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM cc_kv WHERE starts_with(key,'connector-oauth:') AND value->>'user_id'=$1 AND value->>'provider'=$2`, q.user.ID, d.ID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if encrypted != "" {
		if raw, e := s.crypt(encrypted, true); e == nil {
			creds := M{}
			if json.Unmarshal([]byte(raw), &creds) == nil {
				s.revokeConnector(ctx, d, v, creds)
			}
		}
	}
	s.audit(ctx, q.user.ID, "connector.disconnect", "connector", d.ID, M{})
	return M{"status": "disconnected"}, nil
}
func validateConnectorSync(input M, queryFolder string, queryAll bool) (M, error) {
	folder := ""
	all := false
	if raw := input["folder_id"]; raw != nil {
		value, ok := raw.(string)
		if !ok || len([]rune(value)) > 1024 {
			return nil, fail(422, "folder_id must contain at most 1024 characters")
		}
		folder = value
	}
	if raw, ok := input["sync_all"]; ok {
		value, valid := researchBool(raw)
		if !valid {
			return nil, fail(422, "sync_all must be a boolean")
		}
		all = value
	}
	folder = fallback(folder, queryFolder)
	all = all || queryAll
	if len([]rune(folder)) > 1024 {
		return nil, fail(422, "folder_id must contain at most 1024 characters")
	}
	if strings.ContainsFunc(folder, func(r rune) bool { return r < 32 || r == 127 }) {
		return nil, bad("Invalid folder_id")
	}
	if folder == "" && !all {
		return nil, bad("Choose a folder to sync, or set sync_all=true to import the whole connected account")
	}
	return M{"folder_id": nullable(folder), "sync_all": folder == ""}, nil
}
func (s *Server) startConnectorSync(q request) (any, error) {
	d, err := s.connectorDefinition(q.r.PathValue("provider"))
	if err != nil {
		return nil, err
	}
	if err = connectorPermit(q.user, d); err != nil {
		return nil, err
	}
	in := M{}
	if q.r.ContentLength != 0 {
		if err = json.NewDecoder(q.r.Body).Decode(&in); err != nil {
			return nil, bad("Invalid sync request")
		}
	}
	all := false
	if raw := q.r.URL.Query().Get("sync_all"); raw != "" {
		var valid bool
		all, valid = researchBool(raw)
		if !valid {
			return nil, fail(422, "sync_all must be a boolean")
		}
	}
	in, err = validateConnectorSync(in, q.r.URL.Query().Get("folder_id"), all)
	if err != nil {
		return nil, err
	}
	if d.Flat && str(in, "folder_id") != "" {
		return nil, bad(d.Label + " cannot sync a single folder. Request sync_all=true for a full-account import.")
	}
	v, err := s.connectorConfig(q.r.Context(), d.Settings)
	if err != nil {
		return nil, err
	}
	if !s.connectorConfigured(d, v) {
		return nil, bad("Connector not configured")
	}
	account, err := s.connectorCredentials(q.r.Context(), q.user.ID, d, v, "")
	if err != nil {
		return nil, err
	}
	in["provider"], in["generation"] = d.ID, account.Generation
	id, err := s.enqueueTask(q.r.Context(), q.user.ID, "connector.sync", in)
	if err != nil {
		return nil, err
	}
	s.audit(q.r.Context(), q.user.ID, "connector.sync.start", "connector", d.ID, M{"folder_id": in["folder_id"], "sync_all": in["sync_all"], "job_id": id})
	return M{"sync_id": id, "status": "started"}, nil
}
func (s *Server) connectorSyncStatus(q request) (any, error) {
	d, err := s.connectorDefinition(q.r.PathValue("provider"))
	if err != nil {
		return nil, err
	}
	if err = connectorPermit(q.user, d); err != nil {
		return nil, err
	}
	var exists bool
	id := q.r.PathValue("sync_id")
	err = s.db.QueryRowContext(q.r.Context(), `SELECT EXISTS(SELECT 1 FROM cc_jobs WHERE id=$1 AND user_id=$2 AND kind='connector.sync' AND input->>'provider'=$3)`, id, q.user.ID, d.ID).Scan(&exists)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, missing()
	}
	q.r.SetPathValue("id", id)
	result, err := s.getJob(q)
	if err != nil {
		return nil, err
	}
	out := object(result)
	out["sync_id"] = id
	return out, nil
}
