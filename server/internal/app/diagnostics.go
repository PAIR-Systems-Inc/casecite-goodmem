package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

const applicationVersion = "1.3.0-goodmem"

func applicationBuild() string {
	build := fallback(os.Getenv("SOURCE_COMMIT"), os.Getenv("GIT_SHA"))
	if build == "" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range info.Settings {
				if setting.Key == "vcs.revision" {
					build = setting.Value
				}
			}
		}
	}
	build = fallback(build, "unknown")
	return build[:min(12, len(build))]
}

func (s *Server) environment() string {
	if s.cfg.Debug {
		return "development"
	}
	return "production"
}

// Each dependency has its own deadline; an unavailable database cannot use up
// GoodMem's probe budget. No model call or secret is needed to establish readiness.
func (s *Server) dependencyHealth(ctx context.Context) (bool, bool) {
	db, gm := make(chan bool, 1), make(chan bool, 1)
	go func() {
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		db <- s.db.PingContext(probe) == nil
	}()
	go func() {
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		gm <- s.gm.Check(probe) == nil
	}()
	return <-db, <-gm
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	database, goodmem := s.dependencyHealth(r.Context())
	if r.URL.Path == "/ready" {
		issues := []string{}
		if !database {
			issues = append(issues, "Database connection failed")
		}
		if !goodmem {
			issues = append(issues, "GoodMem unavailable")
		}
		if len(issues) > 0 {
			writeJSON(w, 503, M{"ready": false, "issues": issues})
			return
		}
		writeJSON(w, 200, M{"ready": true, "issues": nil})
		return
	}
	status := "healthy"
	if !database || !goodmem {
		status = "degraded"
	}
	out := M{"status": status, "timestamp": nowString(), "started_at": s.startedAt, "version": nil, "build": nil, "environment": nil, "components": nil}
	show := s.cfg.Debug
	if !show && database && sessionToken(r) != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		u, err := s.authenticate(r.WithContext(ctx))
		cancel()
		show = err == nil && u.Role == "admin" && !u.MustChangePassword && !u.MFAEnrollmentRequired
	}
	if show {
		out["version"], out["build"], out["environment"] = applicationVersion, applicationBuild(), s.environment()
		out["components"] = M{
			"vector_db": M{"type": "goodmem", "healthy": goodmem},
			"database":  M{"type": "postgresql", "healthy": database},
			"jobs":      M{"type": "postgresql", "healthy": database},
			"llm":       M{"configured": s.cfg.LLMKey != "" || s.cfg.LLMURL != "", "byok": true},
		}
	}
	// A running process still answers liveness while /ready removes it from service.
	writeJSON(w, 200, out)
}

func (s *Server) metrics(q request) (any, error) {
	q.w.Header().Set("Cache-Control", "no-store")
	ctx, cancel := context.WithTimeout(q.r.Context(), 6*time.Second)
	defer cancel()
	if !s.cfg.Debug {
		u, err := s.authenticate(q.r.WithContext(ctx))
		if err != nil {
			return nil, err
		}
		if u.Role != "admin" {
			return nil, fail(403, "Admin access required")
		}
		if u.MustChangePassword || u.MFAEnrollmentRequired {
			return nil, fail(403, "Complete your account security setup to continue")
		}
	}
	var total, indexed, failed, chunks, known int64
	err := s.db.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER(WHERE data->>'status'='indexed'),count(*) FILTER(WHERE data->>'status'='failed'),COALESCE(sum(CASE WHEN jsonb_typeof(data->'chunk_count')='number' THEN (data->>'chunk_count')::bigint ELSE 0 END),0),count(*) FILTER(WHERE data->>'chunk_count_known'='true') FROM cc_objects WHERE kind='document'`).Scan(&total, &indexed, &failed, &chunks, &known)
	if err != nil {
		return nil, fail(503, "Metrics storage is unavailable")
	}
	var pending, running, failedJobs, sessions, values int64
	err = s.db.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE status='pending'),count(*) FILTER(WHERE status='running'),count(*) FILTER(WHERE status='failed') FROM cc_jobs`).Scan(&pending, &running, &failedJobs)
	if err != nil {
		return nil, err
	}
	if err = s.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM cc_sessions WHERE expires_at>now()),(SELECT count(*) FROM cc_kv WHERE expires_at IS NULL OR expires_at>now())`).Scan(&sessions, &values); err != nil {
		return nil, err
	}
	_, goodmem := s.dependencyHealth(ctx)
	pool := s.db.Stats()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	if strings.Contains(q.r.Header.Get("Accept"), "text/plain") {
		q.w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		gauges := []struct {
			name  string
			value int64
		}{
			{"documents_total", total}, {"documents_indexed", indexed}, {"documents_failed", failed},
			{"jobs_pending", pending}, {"jobs_running", running}, {"jobs_failed", failedJobs},
			{"active_sessions", sessions}, {"stored_values", values}, {"database_connections", int64(pool.OpenConnections)},
			{"process_heap_bytes", int64(mem.HeapAlloc)}, {"process_goroutines", int64(runtime.NumGoroutine())},
		}
		if goodmem {
			gauges = append(gauges, struct {
				name  string
				value int64
			}{"goodmem_healthy", 1})
		} else {
			gauges = append(gauges, struct {
				name  string
				value int64
			}{"goodmem_healthy", 0})
		}
		for _, gauge := range gauges {
			fmt.Fprintf(q.w, "# TYPE casecite_%s gauge\ncasecite_%s %d\n", gauge.name, gauge.name, gauge.value)
		}
		return nil, nil
	}
	return M{
		"timestamp": nowString(), "application": M{"name": "CaseCite GoodMem", "version": applicationVersion, "build": applicationBuild(), "environment": s.environment(), "started_at": s.startedAt},
		"documents": M{"total_count": total, "indexed_count": indexed, "failed_count": failed, "total_chunks": chunks, "chunk_counts_complete": known == total},
		"vector_db": M{"type": "goodmem", "healthy": goodmem},
		"database":  M{"type": "postgresql", "pool_size": pool.MaxOpenConnections, "open_connections": pool.OpenConnections, "in_use": pool.InUse, "idle": pool.Idle},
		"jobs":      M{"type": "postgresql", "pending": pending, "running": running, "failed": failedJobs},
		"storage":   M{"active_sessions": sessions, "values": values},
		"process":   M{"heap_bytes": mem.HeapAlloc, "goroutines": runtime.NumGoroutine()},
	}, nil
}
