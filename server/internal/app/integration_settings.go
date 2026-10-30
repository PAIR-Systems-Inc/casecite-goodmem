package app

import (
	"context"
	"strings"
)

const instanceCourtKey = "instance:courtlistener_api_token"

func (s *Server) instanceCourtToken(ctx context.Context) (string, string, error) {
	var encrypted string
	err := s.kvGet(ctx, instanceCourtKey, &encrypted)
	if notFound(err) {
		key := strings.TrimSpace(s.cfg.CourtListenerToken)
		if key == "" {
			return "", "none", nil
		}
		return key, "env", nil
	}
	if err != nil {
		return "", "", err
	}
	key, err := s.crypt(encrypted, true)
	return key, "instance", err
}

func maskedIntegrationKey(key string) any {
	if key == "" {
		return nil
	}
	if len(key) < 10 {
		return strings.Repeat("•", 8)
	}
	return key[:4] + strings.Repeat("•", 8) + key[len(key)-4:]
}

func (s *Server) courtIntegration(q request) (any, error) {
	if err := permit(q, "admin.settings"); err != nil {
		return nil, err
	}
	status := ""
	if q.r.Method == "POST" {
		in, err := body(q)
		if err != nil {
			return nil, err
		}
		key := strings.TrimSpace(str(in, "api_token"))
		if len(key) < 10 || len(key) > 200 {
			return nil, bad("CourtListener tokens must contain 10–200 characters.")
		}
		for _, r := range key {
			if r < 33 || r > 126 {
				return nil, bad("The CourtListener token contains invalid characters.")
			}
		}
		encrypted, err := s.crypt(key, false)
		if err != nil {
			return nil, err
		}
		if err = s.kvSet(q.r.Context(), instanceCourtKey, encrypted, 0); err != nil {
			return nil, err
		}
		status = "saved"
	} else if q.r.Method == "DELETE" {
		if _, err := s.db.ExecContext(q.r.Context(), `DELETE FROM cc_kv WHERE key=$1`, instanceCourtKey); err != nil {
			return nil, err
		}
		status = "cleared"
	}
	if status != "" {
		s.audit(q.r.Context(), q.user.ID, "settings.update", "integration", "courtlistener", M{"action": status})
	}
	key, source, err := s.instanceCourtToken(q.r.Context())
	if err != nil {
		return nil, err
	}
	out := M{"configured": key != "", "source": source, "masked": maskedIntegrationKey(key)}
	if status != "" {
		out["status"] = status
	}
	return out, nil
}
