package app

import "context"

func (s *Server) jobProgress(ctx context.Context, id, message string, fraction float64) {
	if id == "" {
		return
	}
	_, err := s.db.ExecContext(ctx, `UPDATE cc_jobs SET progress=$2,updated_at=now() WHERE id=$1 AND status='running'`, id, jsonBytes(M{"message": message, "fraction": max(0, min(1, fraction))}))
	if err != nil && ctx.Err() == nil {
		s.log.Warn("job progress write failed", "error", err)
	}
}
