package app

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// RFC 6238, interoperable with standard authenticator applications.
func totpCode(secret string, step int64, digits int) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", err
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := int(sum[len(sum)-1] & 15)
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	mod := uint32(1000000)
	if digits == 8 {
		mod = 100000000
	}
	return fmt.Sprintf("%0*d", digits, value%mod), nil
}

func recoveryHash(code string) string {
	return digest(strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(code), "-", ""), " ", "")))
}
func recoveryCodes() ([]string, []string) {
	codes, hashes := []string{}, []string{}
	for i := 0; i < 10; i++ {
		raw := token()[:20]
		code := raw[:5] + "-" + raw[5:10] + "-" + raw[10:15] + "-" + raw[15:]
		codes = append(codes, code)
		hashes = append(hashes, recoveryHash(code))
	}
	return codes, hashes
}

func (s *Server) factorLimit(ctx context.Context, user string) error {
	n, err := s.countWindow(ctx, "mfa-attempt:"+user, 15*time.Minute)
	if err != nil {
		return err
	}
	if n > 10 {
		return fail(429, "Too many two-factor attempts. Try again later.")
	}
	return nil
}

// The caller holds the user's row lock through commit. Both TOTP counters and
// recovery codes are consumed atomically across processes and concurrent requests.
func (s *Server) consumeFactor(ctx context.Context, tx *sql.Tx, user, code string, allowRecovery bool) error {
	var encrypted string
	var last int64
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT mfa_secret,mfa_last_step,mfa_recovery FROM cc_users WHERE id=$1 AND active FOR UPDATE`, user).Scan(&encrypted, &last, &raw); err != nil {
		return err
	}
	if encrypted == "" {
		return bad("Set up an authenticator first")
	}
	secret, err := s.crypt(encrypted, true)
	if err != nil {
		return err
	}
	code = strings.TrimSpace(code)
	now := time.Now().Unix() / 30
	if len(code) == 6 {
		for step := now - 1; step <= now+1; step++ {
			expected, e := totpCode(secret, step, 6)
			if e != nil {
				return e
			}
			if step > last && subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
				_, err = tx.ExecContext(ctx, `UPDATE cc_users SET mfa_last_step=$1 WHERE id=$2`, step, user)
				return err
			}
		}
	}
	if allowRecovery {
		var hashes []string
		if err = json.Unmarshal(raw, &hashes); err != nil {
			return err
		}
		h := recoveryHash(code)
		for i, v := range hashes {
			if subtle.ConstantTimeCompare([]byte(h), []byte(v)) == 1 {
				hashes = append(hashes[:i], hashes[i+1:]...)
				_, err = tx.ExecContext(ctx, `UPDATE cc_users SET mfa_recovery=$1 WHERE id=$2`, jsonBytes(hashes), user)
				return err
			}
		}
	}
	return fail(401, "Invalid or already used two-factor code")
}

func (s *Server) mfaStatus(q request) (any, error) {
	var enabled bool
	var count int
	err := s.db.QueryRowContext(q.r.Context(), `SELECT mfa_enabled,jsonb_array_length(mfa_recovery) FROM cc_users WHERE id=$1`, q.user.ID).Scan(&enabled, &count)
	return M{"enabled": enabled, "recovery_codes_remaining": count}, err
}

func (s *Server) mfaSetup(q request) (any, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	encrypted, err := s.crypt(secret, false)
	if err != nil {
		return nil, err
	}
	result, err := s.db.ExecContext(q.r.Context(), `UPDATE cc_users SET mfa_secret=$1,mfa_last_step=-1,mfa_recovery='[]' WHERE id=$2 AND NOT mfa_enabled`, encrypted, q.user.ID)
	if err != nil {
		return nil, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, fail(409, "Two-factor authentication is already enabled")
	}
	values := url.Values{"secret": {secret}, "issuer": {"CaseCite"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return M{"secret": secret, "provisioning_uri": "otpauth://totp/" + url.PathEscape("CaseCite:"+q.user.Email) + "?" + values.Encode()}, nil
}

func (s *Server) mfaManage(q request) (any, error) {
	m, err := body(q)
	if err != nil {
		return nil, err
	}
	if err = s.factorLimit(q.r.Context(), q.user.ID); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(q.r.Context(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var enabled bool
	var password string
	if err = tx.QueryRowContext(q.r.Context(), `SELECT mfa_enabled,password_hash FROM cc_users WHERE id=$1 FOR UPDATE`, q.user.ID).Scan(&enabled, &password); err != nil {
		return nil, err
	}
	action := q.r.URL.Path[strings.LastIndex(q.r.URL.Path, "/")+1:]
	if action == "enable" && enabled {
		return nil, fail(409, "Two-factor authentication is already enabled")
	}
	if action != "enable" && !enabled {
		return nil, bad("Two-factor authentication is not enabled")
	}
	if action == "disable" {
		if s.cfg.RequireMFA && password != "" {
			return nil, fail(403, "Your organization requires two-factor authentication")
		}
		if password != "" && bcrypt.CompareHashAndPassword([]byte(password), []byte(str(m, "password"))) != nil {
			return nil, fail(401, "Current password is incorrect")
		}
	}
	if err = s.consumeFactor(q.r.Context(), tx, q.user.ID, str(m, "code"), action == "disable"); err != nil {
		return nil, err
	}
	result := M{"status": "disabled"}
	if action == "disable" {
		_, err = tx.ExecContext(q.r.Context(), `UPDATE cc_users SET mfa_enabled=false,mfa_secret='',mfa_recovery='[]',mfa_last_step=-1 WHERE id=$1`, q.user.ID)
	} else {
		codes, hashes := recoveryCodes()
		_, err = tx.ExecContext(q.r.Context(), `UPDATE cc_users SET mfa_enabled=true,mfa_recovery=$1 WHERE id=$2`, jsonBytes(hashes), q.user.ID)
		result = M{"recovery_codes": codes}
	}
	if err != nil {
		return nil, err
	}
	// A session obtained before enrollment/removal must not retain access.
	if _, err = tx.ExecContext(q.r.Context(), `DELETE FROM cc_sessions WHERE user_id=$1 AND token_hash<>$2`, q.user.ID, digest(sessionToken(q.r))); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.audit(q.r.Context(), q.user.ID, "mfa."+action, "user", q.user.ID, M{})
	return result, nil
}

func (s *Server) mfaVerify(q request) (any, error) {
	m, err := body(q)
	if err != nil {
		return nil, err
	}
	challenge := str(m, "mfa_token")
	if len(challenge) != 64 {
		return nil, fail(401, "Two-factor challenge expired. Sign in again.")
	}
	key := "mfa-challenge:" + digest(challenge)
	var state M
	if err = s.kvGet(q.r.Context(), key, &state); err != nil {
		if notFound(err) {
			return nil, fail(401, "Two-factor challenge expired. Sign in again.")
		}
		return nil, err
	}
	user := str(state, "user_id")
	if err = s.factorLimit(q.r.Context(), user); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(q.r.Context(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var raw []byte
	if err = tx.QueryRowContext(q.r.Context(), `SELECT value FROM cc_kv WHERE key=$1 AND expires_at>now() FOR UPDATE`, key).Scan(&raw); err != nil {
		if notFound(err) {
			return nil, fail(401, "Two-factor challenge expired. Sign in again.")
		}
		return nil, err
	}
	var enabled bool
	if err = tx.QueryRowContext(q.r.Context(), `SELECT mfa_enabled FROM cc_users WHERE id=$1 AND active FOR UPDATE`, user).Scan(&enabled); err != nil {
		return nil, fail(401, "Account is unavailable")
	}
	if !enabled {
		return nil, fail(401, "Two-factor settings changed. Sign in again.")
	}
	if err = s.consumeFactor(q.r.Context(), tx, user, str(m, "code"), true); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(q.r.Context(), `DELETE FROM cc_kv WHERE key=$1`, key); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if _, err = s.db.ExecContext(q.r.Context(), `DELETE FROM cc_kv WHERE key=$1`, "mfa-attempt:"+user); err != nil {
		return nil, err
	}
	return s.signIn(q, User{ID: user})
}
