package app

import "time"

// Refresh tokens rotate in a row-locked transaction. Reuse of a consumed token
// revokes the session family, including the replacement access token.
func (s *Server) refreshSession(q request) (any, error) {
	refresh := ""
	fromCookie := false
	if cookie, err := q.r.Cookie("refresh_token"); err == nil {
		refresh = cookie.Value
		fromCookie = true
	} else {
		m, err := body(q)
		if err != nil {
			return nil, err
		}
		refresh = str(m, "refresh_token")
	}
	if len(refresh) != 64 {
		return nil, fail(401, "Session expired. Please sign in again.")
	}
	tx, err := s.db.BeginTx(q.r.Context(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var user, id, client string
	var expires time.Time
	err = tx.QueryRowContext(q.r.Context(), `SELECT user_id,session_id,expires_at,client FROM cc_sessions WHERE refresh_hash=$1 AND expires_at>now() AND last_activity>now()-$2*interval '1 second' FOR UPDATE`, digest(refresh), int(s.cfg.IdleTTL.Seconds())).Scan(&user, &id, &expires, &client)
	if notFound(err) {
		// Keep consumed hashes until the family's absolute lifetime ends.
		if _, err = tx.ExecContext(q.r.Context(), `DELETE FROM cc_sessions WHERE session_id IN (SELECT session_id FROM cc_refresh_used WHERE token_hash=$1 AND expires_at>now())`, digest(refresh)); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return nil, fail(401, "Session expired. Please sign in again.")
	}
	if err != nil {
		return nil, err
	}
	u, err := s.loadUser(q.r.Context(), user)
	if err != nil {
		if notFound(err) {
			return nil, fail(401, "Account is unavailable")
		}
		return nil, err
	}
	access, next := token(), token()
	if _, err = tx.ExecContext(q.r.Context(), `INSERT INTO cc_refresh_used(token_hash,session_id,expires_at) VALUES($1,$2,$3)`, digest(refresh), id, expires); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(q.r.Context(), `UPDATE cc_sessions SET token_hash=$1,refresh_hash=$2,last_activity=now() WHERE session_id=$3`, digest(access), digest(next), id); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	result := M{"access_token": access, "token_type": "bearer", "expires_in": max(0, int(time.Until(expires).Seconds())), "user": u.public()}
	if fromCookie {
		s.setCookie(q.w, "access_token", access, true, expires)
		s.setCookie(q.w, "refresh_token", next, true, expires)
	} else {
		result["refresh_token"] = next
	}
	return result, nil
}
