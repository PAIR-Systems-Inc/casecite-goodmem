package app

import "slices"

const demoUserID = "dev-user-1"
const demoEmail = "dev@localhost"

// The original development endpoint is available only with DEBUG=true. Use a
// real PostgreSQL identity so sessions and owned resources retain their usual
// foreign keys, while never taking over or upgrading an existing account.
func (s *Server) demoLogin(q request) (any, error) {
	m, err := body(q)
	if err != nil {
		return nil, err
	}
	for _, field := range []string{"email", "password"} {
		if value, exists := m[field]; exists {
			if _, ok := value.(string); !ok {
				return nil, fail(422, field+" must be a string")
			}
		}
	}
	if !s.cfg.Debug {
		s.audit(q.r.Context(), "", "auth.login_failure", "user", "", M{"method": "demo", "reason": "disabled"})
		return nil, fail(403, "Dev login is disabled")
	}
	password := "demo"
	if _, exists := m["password"]; exists {
		password = str(m, "password")
	}
	if str(m, "email") != demoEmail || password != "demo" {
		s.audit(q.r.Context(), "", "auth.login_failure", "user", "", M{"method": "demo", "reason": "invalid_credentials"})
		return nil, fail(401, "Invalid dev credentials")
	}
	_, err = s.db.ExecContext(q.r.Context(), `INSERT INTO cc_users(id,email,name,password_hash,role) VALUES($1,$2,'Dev User','','viewer') ON CONFLICT DO NOTHING`, demoUserID, demoEmail)
	if err != nil {
		return nil, err
	}
	u, err := s.loadUser(q.r.Context(), demoUserID)
	if err != nil {
		return nil, fail(401, "The development account is unavailable")
	}
	return s.signIn(q, u)
}

func (s *Server) restrictDemoUser(u *User) error {
	if u.ID != demoUserID {
		return nil
	}
	if !s.cfg.Debug || u.Email != demoEmail || u.Role != "viewer" || u.HasPassword || u.MFAEnabled || u.MustChangePassword {
		return fail(401, "The development account is unavailable")
	}
	// Editing the normal viewer role cannot elevate a fixed-credential account.
	allowed := defaultPermissions("viewer")
	u.Permissions = slices.DeleteFunc(u.Permissions, func(key string) bool { return !slices.Contains(allowed, key) })
	return nil
}
