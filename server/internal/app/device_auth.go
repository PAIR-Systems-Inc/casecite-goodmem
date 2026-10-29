package app

import (
	"crypto/rand"
	"math/big"
	"net/url"
	"strings"
	"time"
)

func normalizeUserCode(raw string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, strings.ToUpper(raw))
}

func (s *Server) deviceStart(q request) (any, error) {
	n, err := s.countWindow(q.r.Context(), "device-start:"+remoteIP(q.r), time.Minute)
	if err != nil {
		return nil, err
	}
	if n > 10 {
		return nil, fail(429, "Too many device sign-in requests")
	}
	const alphabet = "ACDEFGHJKMNPQRTUVWXY34679"
	for attempt := 0; attempt < 3; attempt++ {
		code := ""
		for i := 0; i < 8; i++ {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
			if err != nil {
				return nil, err
			}
			code += string(alphabet[n.Int64()])
		}
		device := token()
		res, err := s.db.ExecContext(q.r.Context(), `INSERT INTO cc_device_codes(device_hash,user_code_hash) VALUES($1,$2) ON CONFLICT DO NOTHING`, digest(device), digest(code))
		if err != nil {
			return nil, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return nil, err
		}
		if n == 0 {
			continue
		}
		formatted := code[:4] + "-" + code[4:]
		uri := s.cfg.PublicURL + "/device"
		return M{"device_code": device, "user_code": formatted, "verification_uri": uri, "verification_uri_complete": uri + "?user_code=" + url.QueryEscape(formatted), "expires_in": 600, "interval": 5}, nil
	}
	return nil, fail(503, "Unable to allocate a device code. Try again.")
}

func (s *Server) deviceDecision(q request) (any, error) {
	n, err := s.countWindow(q.r.Context(), "device-decision:"+q.user.ID, 15*time.Minute)
	if err != nil {
		return nil, err
	}
	if n > 15 {
		return nil, fail(429, "Too many device code attempts")
	}
	m, err := body(q)
	if err != nil {
		return nil, err
	}
	code := normalizeUserCode(str(m, "user_code"))
	if len(code) != 8 {
		return nil, bad("Enter the eight-character code shown in Word")
	}
	status := "approved"
	if strings.HasSuffix(q.r.URL.Path, "/deny") {
		status = "denied"
	}
	res, err := s.db.ExecContext(q.r.Context(), `UPDATE cc_device_codes SET status=$1,user_id=$2 WHERE user_code_hash=$3 AND status='pending' AND expires_at>now()`, status, q.user.ID, digest(code))
	if err != nil {
		return nil, err
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if changed == 0 {
		return nil, fail(404, "This device code is expired or has already been decided")
	}
	s.audit(q.r.Context(), q.user.ID, "device."+status, "device", "", M{"client": "Word add-in"})
	return M{"status": status, "user_code": code[:4] + "-" + code[4:]}, nil
}

func (s *Server) devicePoll(q request) (any, error) {
	m, err := body(q)
	if err != nil {
		return nil, err
	}
	code := str(m, "device_code")
	if len(code) != 64 {
		return M{"status": "expired_token"}, nil
	}
	tx, err := s.db.BeginTx(q.r.Context(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var user, status string
	var interval int
	var next, expires time.Time
	err = tx.QueryRowContext(q.r.Context(), `SELECT COALESCE(user_id,''),status,interval_seconds,next_poll_at,expires_at FROM cc_device_codes WHERE device_hash=$1 FOR UPDATE`, digest(code)).Scan(&user, &status, &interval, &next, &expires)
	if notFound(err) {
		return M{"status": "expired_token"}, nil
	}
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if !expires.After(now) {
		status = "expired_token"
	} else if status == "denied" {
		status = "access_denied"
	} else if now.Before(next) {
		status = "slow_down"
		interval += 5
	} else if status == "pending" {
		status = "authorization_pending"
	}
	if status == "slow_down" || status == "authorization_pending" {
		_, err = tx.ExecContext(q.r.Context(), `UPDATE cc_device_codes SET interval_seconds=$1,next_poll_at=$2 WHERE device_hash=$3`, interval, now.Add(time.Duration(interval)*time.Second), digest(code))
	} else {
		_, err = tx.ExecContext(q.r.Context(), `DELETE FROM cc_device_codes WHERE device_hash=$1`, digest(code))
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if status != "approved" {
		return M{"status": status}, nil
	}
	u, err := s.loadUser(q.r.Context(), user)
	if err != nil {
		return nil, fail(403, "Account is unavailable")
	}
	if u.MustChangePassword || u.MFAEnrollmentRequired {
		return nil, fail(403, "Complete your account setup in the web application before signing into Word")
	}
	result, err := s.issueSession(q, u, "Word add-in")
	if err != nil {
		return nil, err
	}
	result["status"] = "approved"
	return result, nil
}
