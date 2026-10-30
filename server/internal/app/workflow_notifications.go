package app

import (
	"context"
	"encoding/json"
	"html"
	"net/url"
	"strings"
)

var workflowSubjects = map[string]string{"completed": "Workflow finished", "failed": "Workflow failed", "awaiting_approval": "Workflow needs your approval", "rejected": "Workflow rejected"}

func workflowEmail(event, name, link string, data M) string {
	escape := html.EscapeString
	lines := []string{"<p>The workflow <strong>" + escape(name) + "</strong> started " + escape(str(data, "started_at")) + " is <strong>" + escape(strings.ReplaceAll(event, "_", " ")) + "</strong>.</p>"}
	if event == "awaiting_approval" {
		lines = append(lines, "<p>It is waiting at the step <strong>"+escape(fallback(str(object(data["awaiting"]), "title"), "Approval"))+"</strong> for your decision.</p>")
	}
	if message := str(data, "error"); message != "" {
		lines = append(lines, "<p>Error: "+escape(charSlice(message, 0, 500))+"</p>")
	}
	if note := str(data, "decision_note"); note != "" {
		lines = append(lines, "<p>Note: "+escape(charSlice(note, 0, 1000))+"</p>")
	}
	lines = append(lines, `<p><a href="`+escape(link)+`" class="button">Open the run</a></p>`)
	return `<!DOCTYPE html><html><head><style>body{font-family:Arial,sans-serif;line-height:1.6;color:#333}.container{max-width:600px;margin:auto;padding:20px}.button{display:inline-block;padding:12px 24px;background:#1a365d;color:white;text-decoration:none;border-radius:6px}.footer{margin-top:30px;font-size:12px;color:#666}</style></head><body><div class="container"><h1>` + escape(workflowSubjects[event]) + `</h1>` + strings.Join(lines, "\n") + `<div class="footer"><p>Every workflow output is model output. Verify it against the source documents and current law before relying on it.</p><p>CaseCite GoodMem. You can turn these emails off under Settings.</p></div></div></body></html>`
}

func (s *Server) deliverWorkflowNotification(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id int64
	var rid, event string
	var b []byte
	err = tx.QueryRowContext(ctx, `SELECT id,run_id,event,data FROM cc_workflow_events WHERE processed_at IS NULL ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id, &rid, &event, &b)
	if notFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if s.cfg.WorkflowNotifications && s.cfg.SMTPHost != "" && s.cfg.SMTPFrom != "" {
		data := M{}
		if err = json.Unmarshal(b, &data); err != nil {
			return err
		}
		var name string
		if err = tx.QueryRowContext(ctx, `SELECT data->>'name' FROM cc_objects WHERE kind='workflow' AND id=$1`, str(data, "workflow_id")).Scan(&name); err != nil {
			return err
		}
		ids := []string{str(data, "runner_id")}
		if event == "awaiting_approval" {
			ids = append(ids, stringsOf(object(data["awaiting"])["approver_ids"])...)
		}
		for _, user := range uniqueWorkflowStrings(ids) {
			settings, e := s.userSettings(ctx, user)
			if e != nil {
				return e
			}
			if settings["workflow_email_notifications"] == false {
				continue
			}
			var email string
			// Revoked users must not receive the workflow name or decision notes.
			e = tx.QueryRowContext(ctx, `SELECT u.email FROM cc_users u JOIN cc_objects o ON o.kind='workflow' AND o.id=$2 WHERE u.id=$1 AND u.active AND `+visibleSQL, user, str(data, "workflow_id")).Scan(&email)
			if notFound(e) {
				continue
			}
			if e != nil {
				return e
			}
			link := strings.TrimRight(s.cfg.PublicURL, "/") + "/workflows/" + url.PathEscape(str(data, "workflow_id")) + "?run=" + url.QueryEscape(rid)
			subject := charSlice("[CaseCite GoodMem] "+workflowSubjects[event]+": "+strings.Join(strings.Fields(name), " "), 0, 200)
			if e = s.queueEmailMessage(ctx, tx, outboundEmail{To: email, Subject: subject, Body: workflowEmail(event, name, link, data), HTML: true}); e != nil {
				return e
			}
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE cc_workflow_events SET processed_at=now() WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit()
}
