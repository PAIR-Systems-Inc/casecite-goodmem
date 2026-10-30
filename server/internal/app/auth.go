package app

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID, Email, Name, Role                          string
	MustChangePassword                             bool
	MFAEnabled, MFAEnrollmentRequired, HasPassword bool
	Permissions                                    []string
}

func (u User) public() M {
	return M{"id": u.ID, "email": u.Email, "name": u.Name, "roles": []string{u.Role}, "must_change_password": u.MustChangePassword, "mfa_enabled": u.MFAEnabled, "mfa_enrollment_required": u.MFAEnrollmentRequired, "permissions": u.Permissions}
}

func (s *Server) loadUser(ctx context.Context, id string) (User, error) {
	u := User{}
	err := s.db.QueryRowContext(ctx, `SELECT id,email,name,role,must_change_password,mfa_enabled,password_hash<>'' FROM cc_users WHERE id=$1 AND active`, id).Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.MustChangePassword, &u.MFAEnabled, &u.HasPassword)
	if err != nil {
		return u, err
	}
	u.MFAEnrollmentRequired = s.cfg.RequireMFA && u.HasPassword && !u.MFAEnabled
	u.Permissions, err = s.rolePermissions(ctx, u.Role)
	if err == nil {
		err = s.restrictDemoUser(&u)
	}
	return u, err
}
func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func (s *Server) csrfValue() string {
	n := token()
	h := hmac.New(sha256.New, []byte(s.cfg.Secret))
	h.Write([]byte(n))
	return n + "." + hex.EncodeToString(h.Sum(nil))
}
func (s *Server) validCSRF(v string) bool {
	parts := strings.Split(v, ".")
	if len(parts) != 2 || len(parts[0]) != 64 {
		return false
	}
	h := hmac.New(sha256.New, []byte(s.cfg.Secret))
	h.Write([]byte(parts[0]))
	sig, err := hex.DecodeString(parts[1])
	return err == nil && hmac.Equal(h.Sum(nil), sig)
}
func (s *Server) setCookie(w http.ResponseWriter, name, value string, httpOnly bool, expiry time.Time) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: httpOnly, Secure: !s.cfg.Debug, SameSite: http.SameSiteLaxMode, Expires: expiry})
}
func sessionToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	if c, e := r.Cookie("access_token"); e == nil {
		return c.Value
	}
	return ""
}
func (s *Server) authenticate(r *http.Request) (User, error) {
	u := User{}
	t := sessionToken(r)
	if len(t) != 64 {
		return u, fail(401, "Please sign in")
	}
	err := s.db.QueryRowContext(r.Context(), `UPDATE cc_sessions ss SET last_activity=now() FROM cc_users u WHERE ss.token_hash=$1 AND ss.user_id=u.id AND u.active AND ss.expires_at>now() AND ss.last_activity>now()-$2*interval '1 second' RETURNING u.id`, digest(t), int(s.cfg.IdleTTL.Seconds())).Scan(&u.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return u, fail(401, "Session expired. Please sign in again.")
	}
	if err != nil {
		return u, fail(503, "Session storage is unavailable")
	}
	return s.loadUser(r.Context(), u.ID)
}
func (s *Server) signIn(q request, u User) (any, error) {
	u, err := s.loadUser(q.r.Context(), u.ID)
	if err != nil {
		return nil, err
	}
	return s.issueSession(q, u, "Web")
}

func (s *Server) issueSession(q request, u User, client string) (M, error) {
	t, refresh := token(), token()
	expires := time.Now().Add(s.cfg.SessionTTL)
	_, err := s.db.ExecContext(q.r.Context(), `INSERT INTO cc_sessions(token_hash,user_id,expires_at,ip_address,refresh_hash,client) VALUES($1,$2,$3,$4,$5,$6)`, digest(t), u.ID, expires, remoteIP(q.r), digest(refresh), client)
	if err != nil {
		return nil, err
	}
	if client == "Web" {
		s.setCookie(q.w, "access_token", t, true, expires)
		s.setCookie(q.w, "refresh_token", refresh, true, expires)
		s.setCookie(q.w, "_csrf", s.csrfValue(), false, expires)
	}
	_, err = s.db.ExecContext(q.r.Context(), `UPDATE cc_users SET last_login=now() WHERE id=$1`, u.ID)
	if err != nil {
		return nil, err
	}
	s.audit(q.r.Context(), u.ID, "auth.login", "user", u.ID, M{})
	result := M{"access_token": t, "token_type": "bearer", "expires_in": int(s.cfg.SessionTTL.Seconds()), "user": u.public()}
	if client != "Web" {
		result["refresh_token"] = refresh
	}
	return result, nil
}
func (s *Server) register(q request) (any, error) {
	m, err := body(q)
	if err != nil {
		return nil, err
	}
	email := strings.ToLower(strings.TrimSpace(str(m, "email")))
	name := strings.TrimSpace(str(m, "name"))
	password := str(m, "password")
	if !strings.Contains(email, "@") || len(email) > 254 || name == "" || len(name) > 200 {
		return nil, bad("A valid email and name are required")
	}
	if !validPassword(password) {
		return nil, bad(strings.Join(passwordErrors(password), ". "))
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(q.r.Context(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(q.r.Context(), "SELECT pg_advisory_xact_lock(734280002)"); err != nil {
		return nil, err
	}
	var count int
	if err = tx.QueryRowContext(q.r.Context(), `SELECT count(*) FROM cc_users WHERE id<>$1 OR email<>$2 OR role<>'viewer' OR password_hash<>''`, demoUserID, demoEmail).Scan(&count); err != nil {
		return nil, err
	}
	role := "attorney"
	if count == 0 {
		if s.cfg.BootstrapToken == "" || subtle.ConstantTimeCompare([]byte(str(m, "bootstrap_token")), []byte(s.cfg.BootstrapToken)) != 1 {
			return nil, fail(403, "A valid bootstrap token is required for the first administrator account")
		}
		role = "admin"
	} else {
		return nil, fail(403, "Ask your administrator to invite you")
	}
	u := User{ID: uuid(), Email: email, Name: name, Role: role}
	if _, err = tx.ExecContext(q.r.Context(), `INSERT INTO cc_users(id,email,name,password_hash,role,company) VALUES($1,$2,$3,$4,$5,$6)`, u.ID, email, name, string(hash), role, str(m, "company")); err != nil {
		return nil, bad("This email cannot be registered")
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.signIn(q, u)
}
func (s *Server) login(q request) (any, error) {
	m, err := body(q)
	if err != nil {
		return nil, err
	}
	email := strings.ToLower(strings.TrimSpace(str(m, "email")))
	n, err := s.countWindow(q.r.Context(), "login:"+digest(email), 15*time.Minute)
	if err != nil {
		return nil, err
	}
	if n > 12 {
		return nil, fail(429, "Too many sign-in attempts for this account")
	}
	u := User{}
	var hash string
	err = s.db.QueryRowContext(q.r.Context(), `SELECT id,email,name,role,must_change_password,password_hash FROM cc_users WHERE email=$1 AND active`, email).Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.MustChangePassword, &hash)
	if err != nil && !notFound(err) {
		return nil, err
	}
	if hash == "" {
		hash = "$2a$10$7EqJtq98hPqEX7fNZaFWoO5gQAvGvR9mxckQz06YSLKq4gYF0z6qO"
	}
	if check := bcrypt.CompareHashAndPassword([]byte(hash), []byte(str(m, "password"))); check != nil || u.ID == "" {
		return nil, fail(401, "Invalid email or password")
	}
	if _, err = s.db.ExecContext(q.r.Context(), "DELETE FROM cc_kv WHERE key=$1", "login:"+digest(email)); err != nil {
		return nil, err
	}
	u, err = s.loadUser(q.r.Context(), u.ID)
	if err != nil {
		return nil, err
	}
	if u.MFAEnabled {
		challenge := token()
		if err = s.kvSet(q.r.Context(), "mfa-challenge:"+digest(challenge), M{"user_id": u.ID}, 5*time.Minute); err != nil {
			return nil, err
		}
		return M{"mfa_required": true, "mfa_token": challenge}, nil
	}
	return s.signIn(q, u)
}
func (s *Server) logout(q request) (any, error) {
	query := "DELETE FROM cc_sessions WHERE token_hash=$1"
	arg := digest(sessionToken(q.r))
	if strings.HasSuffix(q.r.URL.Path, "/all") {
		query = "DELETE FROM cc_sessions WHERE user_id=$1"
		arg = q.user.ID
	}
	if _, err := s.db.ExecContext(q.r.Context(), query, arg); err != nil {
		return nil, err
	}
	s.setCookie(q.w, "access_token", "", true, time.Unix(1, 0))
	s.setCookie(q.w, "refresh_token", "", true, time.Unix(1, 0))
	return M{"status": "logged_out"}, nil
}
func (s *Server) sessions(q request) (any, error) {
	rows, err := s.db.QueryContext(q.r.Context(), `SELECT created_at,last_activity,expires_at,ip_address FROM cc_sessions WHERE user_id=$1 AND expires_at>now() ORDER BY created_at DESC`, q.user.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []M{}
	for rows.Next() {
		var a, b, c time.Time
		var ip string
		if err = rows.Scan(&a, &b, &c, &ip); err != nil {
			return nil, err
		}
		out = append(out, M{"created_at": a, "last_activity": b, "expires_at": c, "ip_address": ip})
	}
	return M{"sessions": out}, rows.Err()
}
func (s *Server) changePassword(q request) (any, error) {
	m, err := body(q)
	if err != nil {
		return nil, err
	}
	if !validPassword(str(m, "new_password")) {
		return nil, bad(strings.Join(passwordErrors(str(m, "new_password")), ". "))
	}
	var hash string
	if err = s.db.QueryRowContext(q.r.Context(), "SELECT password_hash FROM cc_users WHERE id=$1", q.user.ID).Scan(&hash); err != nil {
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(str(m, "current_password"))) != nil {
		return nil, fail(401, "Current password is incorrect")
	}
	b, err := bcrypt.GenerateFromPassword([]byte(str(m, "new_password")), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(q.r.Context(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(q.r.Context(), `UPDATE cc_users SET password_hash=$1,must_change_password=false,password_changed_at=now() WHERE id=$2`, string(b), q.user.ID); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(q.r.Context(), `DELETE FROM cc_sessions WHERE user_id=$1 AND token_hash<>$2`, q.user.ID, digest(sessionToken(q.r))); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(q.r.Context(), `DELETE FROM cc_kv WHERE key LIKE 'mfa-challenge:%' AND value->>'user_id'=$1`, q.user.ID); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(q.r.Context(), `DELETE FROM cc_device_codes WHERE user_id=$1`, q.user.ID); err != nil {
		return nil, err
	}
	return M{"status": "changed"}, tx.Commit()
}
func (s *Server) crypt(v string, decrypt bool) (string, error) {
	k := sha256.Sum256([]byte(s.cfg.Secret))
	block, err := aes.NewCipher(k[:])
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if decrypt {
		b, err := base64.StdEncoding.DecodeString(v)
		if err != nil || len(b) < aead.NonceSize() {
			return "", errors.New("invalid encrypted credential")
		}
		plain, err := aead.Open(nil, b[:aead.NonceSize()], b[aead.NonceSize():], []byte("casecite-goodmem"))
		return string(plain), err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(aead.Seal(nonce, nonce, []byte(v), []byte("casecite-goodmem"))), nil
}
