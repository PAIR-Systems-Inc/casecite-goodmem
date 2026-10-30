package app

import "time"

func (s *Server) routes() {
	s.api("GET /api/v1/csrf-token", true, func(q request) (any, error) {
		t := s.csrfValue()
		s.setCookie(q.w, "_csrf", t, false, time.Now().Add(8*time.Hour))
		return M{"csrf_token": t}, nil
	})
	s.api("GET /api/v1/config", true, func(q request) (any, error) {
		var count int
		err := s.db.QueryRowContext(q.r.Context(), `SELECT count(*) FROM cc_users`).Scan(&count)
		return M{"setup_required": count == 0, "edition": "CaseCite GoodMem", "self_registration": false}, err
	})
	s.api("POST /api/v1/auth/register", true, s.register)
	s.api("POST /api/v1/auth/login", true, s.login)
	s.api("GET /api/v1/auth/me", false, func(q request) (any, error) { return q.user.public(), nil })
	s.api("POST /api/v1/auth/logout", false, s.logout)
	s.api("POST /api/v1/auth/logout/all", false, s.logout)
	s.api("GET /api/v1/auth/sessions", false, s.sessions)
	s.api("POST /api/v1/auth/change-password", false, s.changePassword)
	s.identityRoutes()
	s.platformRoutes()
	s.matterRoutes()
	s.documentWorkspaceRoutes()
	s.contractRoutes()
	s.workspaceSessionRoutes()
	s.reviewTableRoutes()
	s.workflowRoutes()
	s.api("GET /api/v1/settings", false, s.settings)
	s.api("PUT /api/v1/settings", false, s.settings)
	s.api("PUT /api/v1/keys/{provider}", false, s.saveKey)
	s.api("DELETE /api/v1/keys/{provider}", false, s.saveKey)
	s.api("GET /api/v1/users", false, s.users)
	s.api("POST /api/v1/users", false, s.createUser)
	s.api("PATCH /api/v1/users/{id}", false, s.updateUser)
	s.api("GET /api/v1/audit", false, s.auditEvents)
	s.api("GET /api/v1/matters", false, s.listMatters)
	s.api("POST /api/v1/matters", false, s.createMatter)
	s.api("DELETE /api/v1/matters/{id}", false, s.deleteMatter)
	s.api("GET /api/v1/matters/{id}/members", false, s.matterMembers)
	s.api("POST /api/v1/matters/{id}/members", false, s.addMember)
	s.api("DELETE /api/v1/matters/{id}/members/{user}", false, s.removeMember)
	s.api("GET /api/v1/documents", false, s.listDocuments)
	s.api("POST /api/v1/documents", false, s.upload)
	s.api("GET /api/v1/documents/{id}", false, func(q request) (any, error) {
		d, err := s.document(q)
		if err != nil {
			return nil, err
		}
		return s.refreshDocument(q.r.Context(), d), nil
	})
	s.api("DELETE /api/v1/documents/{id}", false, s.deleteDocument)
	s.api("PATCH /api/v1/documents/{id}", false, s.moveDocument)
	s.api("GET /api/v1/documents/{id}/content", false, s.documentContent)
	s.api("GET /api/v1/documents/{id}/file", false, s.documentFile)
	s.api("GET /api/v1/documents/{id}/pages", false, s.pages)
	s.api("GET /api/v1/documents/{id}/pages/{index}/image", false, s.page)
	s.api("GET /api/v1/jobs", false, s.listJobs)
	s.api("POST /api/v1/jobs", false, s.createJob)
	s.api("GET /api/v1/jobs/{id}", false, s.getJob)
	s.api("DELETE /api/v1/jobs/{id}", false, s.cancelJob)
	s.api("GET /api/v1/results", false, s.listResults)
	s.api("GET /api/v1/results/{id}", false, s.getResult)
	s.api("DELETE /api/v1/results/{id}", false, s.deleteResult)
	s.api("GET /api/v1/results/{id}/export", false, s.exportResult)
	s.api("PATCH /api/v1/results/{id}/cells", false, s.reviewCell)
}
