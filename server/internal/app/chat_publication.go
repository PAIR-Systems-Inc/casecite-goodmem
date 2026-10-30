package app

import (
	"context"
	"database/sql"
	"errors"
)

type chatExchange struct {
	User, Session, Matter, Query string
	JudgeID, JudgeVersion        string
	Personal                     bool
	NewSession                   bool
	Answer, Revisions            M
}

func (s *Server) failJobPublication(ctx context.Context, id, lease string, err error) {
	status, message := 500, "The job result could not be saved. Check its history before trying again."
	var ae *apiError
	if errors.As(err, &ae) {
		status, message = ae.status, ae.message
	}
	s.log.Warn("job publication failed", "job", id, "error", err)
	_, saveErr := s.db.ExecContext(ctx, `UPDATE cc_jobs SET status='failed',result=NULL,error=$3,lease_until=NULL,updated_at=now(),progress=progress || jsonb_build_object('error_status',$4::int) WHERE id=$1 AND lease_token=$2 AND status='running'`, id, lease, message, status)
	if saveErr != nil {
		s.log.Error("chat failure save failed", "job", id, "error", saveErr)
	}
}

func (s *Server) stageChatPublication(ctx context.Context, exchange *chatExchange) error {
	outputs, ok := ctx.Value(jobOutputsKey{}).(*jobOutputs)
	if !ok {
		return fail(500, "Chat publication requires a durable job")
	}
	outputs.Lock()
	defer outputs.Unlock()
	outputs.Chat = exchange
	return nil
}

// Called inside the transaction holding the job's completion lock. Either the
// result and both messages become visible together, or none of them do.
func (s *Server) publishChat(ctx context.Context, tx *sql.Tx, exchange *chatExchange) error {
	if err := s.validateJudgeChatPublication(ctx, tx, exchange); err != nil {
		return err
	}
	if exchange.Session != "" {
		if exchange.NewSession {
			_, err := tx.ExecContext(ctx, `INSERT INTO cc_objects(kind,id,owner_id,data) VALUES('chat_session',$1,$2,$3)`, exchange.Session, exchange.User, jsonBytes(M{"title": charSlice(exchange.Query, 0, 60), "message_count": 0}))
			if err != nil {
				return err
			}
		} else {
			var matter string
			err := tx.QueryRowContext(ctx, `SELECT COALESCE(o.matter_id,'') FROM cc_objects o WHERE `+visibleSQL+` AND o.kind='chat_session' AND o.id=$2 FOR UPDATE`, exchange.User, exchange.Session).Scan(&matter)
			if notFound(err) {
				return fail(404, "The conversation was deleted or is no longer accessible")
			}
			if err != nil {
				return err
			}
			if matter != exchange.Matter {
				return fail(409, "The conversation's matter changed while answering. Submit the question again.")
			}
		}
	}
	for _, id := range stringsOf(exchange.Answer["access_document_ids"]) {
		doc, err := scanObject(tx.QueryRowContext(ctx, `SELECT o.id,o.owner_id,COALESCE(o.matter_id,''),o.data,o.created_at,o.updated_at FROM cc_objects o WHERE `+visibleSQL+` AND o.kind='document' AND o.id=$2 FOR SHARE`, exchange.User, id))
		if notFound(err) {
			return fail(404, "An answer source was deleted or is no longer accessible")
		}
		if err != nil {
			return err
		}
		if str(doc, "memory_id") != str(exchange.Revisions, id) {
			return fail(409, "An answer source changed while answering. Submit the question again.")
		}
		if !chatSessionAllowsDoc(exchange.Matter, exchange.Personal, exchange.User, doc) {
			return fail(409, "An answer source was moved outside this conversation's matter")
		}
	}
	if exchange.Session == "" {
		return nil
	} // standalone strategy has no transcript
	question := M{"role": "user", "content": exchange.Query, "citations": []M{}, "strategy": nil, "stats": nil}
	answer := M{"role": "assistant", "content": exchange.Answer["content"], "citations": exchange.Answer["citations"], "strategy": exchange.Answer["strategy"], "stats": exchange.Answer["stats"], "access_document_ids": exchange.Answer["access_document_ids"]}
	if exchange.Answer["judge_context"] != nil {
		answer["judge_context"] = exchange.Answer["judge_context"]
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO cc_chat_messages(id,session_id,data) VALUES($1,$2,$3),($4,$2,$5)`, uuid(), exchange.Session, jsonBytes(question), exchange.Answer["id"], jsonBytes(answer))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE cc_objects SET updated_at=now(),data=jsonb_set(data,'{message_count}',to_jsonb(COALESCE((data->>'message_count')::int,0)+2)) WHERE kind='chat_session' AND id=$1`, exchange.Session)
	return err
}

func (s *Server) deleteChatSession(q request) (any, error) {
	ctx, id := q.r.Context(), q.r.PathValue("id")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	allowed := `kind='chat_session' AND id=$1 AND (owner_id=$2 OR EXISTS(SELECT 1 FROM cc_matters m WHERE m.id=cc_objects.matter_id AND m.owner_id=$2))`
	var exists string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM cc_objects WHERE `+allowed, id, q.user.ID).Scan(&exists); err != nil {
		return nil, err
	}
	// Match worker lock order: jobs first, then the session. Removing pending
	// jobs also prevents a finished provider call from resurrecting history.
	if _, err = tx.ExecContext(ctx, `DELETE FROM cc_jobs WHERE kind='chat.reply' AND input->>'session_id'=$1`, id); err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM cc_objects WHERE `+allowed, id, q.user.ID)
	if err != nil {
		return nil, err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return nil, fail(404, "Session not found")
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	s.audit(ctx, q.user.ID, "chat_session.delete", "chat_session", id, M{})
	return M{"status": "deleted", "id": id}, nil
}
