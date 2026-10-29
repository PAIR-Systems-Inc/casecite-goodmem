package app

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

type outboundEmail struct{ To, Subject, Body string }

func (s *Server) queueEmail(ctx context.Context, tx *sql.Tx, to, subject, body string) error {
	if _, err := mail.ParseAddress(to); err != nil {
		return bad("Invalid recipient email address")
	}
	encrypted, err := s.crypt(string(jsonBytes(outboundEmail{To: to, Subject: subject, Body: body})), false)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO cc_outbox(id,payload,owner_id) VALUES($1,$2,(SELECT id FROM cc_users WHERE email=$3))`, uuid(), encrypted, to)
	return err
}

func (s *Server) emailWorker() {
	defer s.wg.Done()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
		if err := s.deliverNextEmail(s.ctx); err != nil && s.ctx.Err() == nil {
			s.log.Warn("email delivery attempt failed", "error", err)
		}
	}
}

func (s *Server) deliverNextEmail(ctx context.Context) error {
	var id, payload string
	var attempts int
	err := s.db.QueryRowContext(ctx, `UPDATE cc_outbox SET status='sending',lease_until=now()+interval '1 minute',attempts=attempts+1 WHERE id=(SELECT id FROM cc_outbox WHERE (status='pending' OR (status='sending' AND lease_until<now())) AND next_attempt_at<=now() ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id,payload,attempts`).Scan(&id, &payload, &attempts)
	if notFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	plain, err := s.crypt(payload, true)
	var message outboundEmail
	if err == nil {
		err = json.Unmarshal([]byte(plain), &message)
	}
	if err == nil {
		err = s.sendEmail(ctx, message)
	}
	if err == nil {
		_, err = s.db.ExecContext(ctx, `UPDATE cc_outbox SET status='sent',payload='',sent_at=now(),lease_until=NULL,error=NULL WHERE id=$1`, id)
		return err
	}
	status := "pending"
	if attempts >= 6 {
		status = "failed"
	}
	_, saveErr := s.db.ExecContext(ctx, `UPDATE cc_outbox SET status=$1,next_attempt_at=now()+$2*interval '1 second',lease_until=NULL,error=$3 WHERE id=$4`, status, min(3600, 30*(1<<min(attempts, 7))), "Delivery failed; check SMTP configuration and connectivity", id)
	if saveErr != nil {
		return saveErr
	}
	return err
}

func (s *Server) sendEmail(ctx context.Context, message outboundEmail) error {
	if s.cfg.SMTPHost == "" || s.cfg.SMTPFrom == "" {
		return errors.New("SMTP_HOST and SMTP_FROM are required for email delivery")
	}
	from, err := mail.ParseAddress(s.cfg.SMTPFrom)
	if err != nil {
		return errors.New("SMTP_FROM is invalid")
	}
	to, err := mail.ParseAddress(message.To)
	if err != nil {
		return errors.New("recipient is invalid")
	}
	if strings.ContainsAny(message.Subject, "\r\n") {
		return errors.New("email subject contains a newline")
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(s.cfg.SMTPHost, s.cfg.SMTPPort))
	if err != nil {
		return err
	}
	defer conn.Close()
	if err = conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return err
	}
	tlsConfig := &tls.Config{ServerName: s.cfg.SMTPHost, MinVersion: tls.VersionTLS12}
	if s.cfg.SMTPTLS {
		secure := tls.Client(conn, tlsConfig)
		if err = secure.HandshakeContext(ctx); err != nil {
			return err
		}
		conn = secure
	}
	client, err := smtp.NewClient(conn, s.cfg.SMTPHost)
	if err != nil {
		return err
	}
	defer client.Close()
	if !s.cfg.SMTPTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP server must support STARTTLS or SMTP_TLS=true")
		}
		if err = client.StartTLS(tlsConfig); err != nil {
			return err
		}
	}
	if s.cfg.SMTPUser != "" {
		if err = client.Auth(smtp.PlainAuth("", s.cfg.SMTPUser, s.cfg.SMTPPassword, s.cfg.SMTPHost)); err != nil {
			return err
		}
	}
	if err = client.Mail(from.Address); err != nil {
		return err
	}
	if err = client.Rcpt(to.Address); err != nil {
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	body := strings.ReplaceAll(strings.ReplaceAll(message.Body, "\r\n", "\n"), "\n", "\r\n")
	_, err = fmt.Fprintf(w, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%s@casecite>\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n", from.String(), to.String(), mime.QEncoding.Encode("UTF-8", message.Subject), time.Now().Format(time.RFC1123Z), uuid(), body)
	if err != nil {
		return err
	}
	if err = w.Close(); err != nil {
		return err
	}
	return client.Quit()
}
