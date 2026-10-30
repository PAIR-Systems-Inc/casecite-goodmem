package app

import (
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const resetRequestedMessage = "If an account with that email exists, a password reset link has been sent."

func (s *Server) forgotPassword(q request) (any, error) {
	m, err := body(q)
	if err != nil {
		return nil, err
	}
	email := strings.ToLower(strings.TrimSpace(str(m, "email")))
	n, err := s.countWindow(q.r.Context(), "password-reset-request:"+digest(email), time.Hour)
	if err != nil {
		return nil, err
	}
	if n > 3 {
		return M{"message": resetRequestedMessage}, nil
	}
	var id string
	err = s.db.QueryRowContext(q.r.Context(), `SELECT id FROM cc_users WHERE email=$1 AND active AND password_hash<>''`, email).Scan(&id)
	if notFound(err) {
		return M{"message": resetRequestedMessage}, nil
	}
	if err != nil {
		return nil, err
	}
	reset := token()
	tx, err := s.db.BeginTx(q.r.Context(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Lock the account so requesting two links concurrently cannot leave two valid tokens.
	if err = tx.QueryRowContext(q.r.Context(), `SELECT id FROM cc_users WHERE id=$1 FOR UPDATE`, id).Scan(&id); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(q.r.Context(), `DELETE FROM cc_kv WHERE key LIKE 'password-reset:%' AND value->>'user_id'=$1`, id); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(q.r.Context(), `INSERT INTO cc_kv(key,value,expires_at) VALUES($1,$2,now()+interval '1 hour')`, "password-reset:"+digest(reset), jsonBytes(M{"user_id": id})); err != nil {
		return nil, err
	}
	message := "A password reset was requested for your CaseCite account. Open this link within one hour:\r\n\r\n" + s.cfg.PublicURL + "/reset-password?token=" + reset + "\r\n\r\nIf you did not request this, you can ignore this email."
	if err = s.queueEmail(q.r.Context(), tx, email, "Reset your CaseCite password", message); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.audit(q.r.Context(), id, "auth.password_reset_requested", "user", id, M{})
	return M{"message": resetRequestedMessage}, nil
}

func (s *Server) verifyReset(q request) (any, error) {
	m, err := body(q)
	if err != nil {
		return nil, err
	}
	var valid bool
	err = s.db.QueryRowContext(q.r.Context(), `SELECT EXISTS(SELECT 1 FROM cc_kv k JOIN cc_users u ON u.id=k.value->>'user_id' WHERE k.key=$1 AND k.expires_at>now() AND u.active AND u.password_hash<>'')`, "password-reset:"+digest(str(m, "token"))).Scan(&valid)
	return M{"valid": valid}, err
}

func (s *Server) resetPassword(q request) (any, error) {
	m, err := body(q)
	if err != nil {
		return nil, err
	}
	password := str(m, "new_password")
	if !validPassword(password) {
		return nil, bad(strings.Join(passwordErrors(password), ". "))
	}
	key := "password-reset:" + digest(str(m, "token"))
	var state M
	if err = s.kvGet(q.r.Context(), key, &state); err != nil {
		if notFound(err) {
			return nil, bad("This password reset link is invalid or expired")
		}
		return nil, err
	}
	user := str(state, "user_id")
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(q.r.Context(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var id string
	if err = tx.QueryRowContext(q.r.Context(), `SELECT id FROM cc_users WHERE id=$1 AND active AND password_hash<>'' FOR UPDATE`, user).Scan(&id); err != nil {
		return nil, bad("This password reset link is invalid or expired")
	}
	res, err := tx.ExecContext(q.r.Context(), `DELETE FROM cc_kv WHERE key=$1 AND expires_at>now()`, key)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n != 1 {
		return nil, bad("This password reset link is invalid or expired")
	}
	if _, err = tx.ExecContext(q.r.Context(), `UPDATE cc_users SET password_hash=$1,must_change_password=false,password_changed_at=now() WHERE id=$2`, string(hash), id); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(q.r.Context(), `DELETE FROM cc_sessions WHERE user_id=$1`, id); err != nil {
		return nil, err
	}
	// An outstanding first-factor challenge must not survive a password reset.
	if _, err = tx.ExecContext(q.r.Context(), `DELETE FROM cc_kv WHERE key LIKE 'mfa-challenge:%' AND value->>'user_id'=$1`, id); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.audit(q.r.Context(), id, "auth.password_reset", "user", id, M{})
	return M{"message": "Password reset successfully. Please sign in with your new password."}, nil
}
