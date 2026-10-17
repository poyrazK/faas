package functionsql

// adr: 1577

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/e2etest/functionmigration"
)

// Sessions observes PostgreSQL's own backend identity, independently of the
// guest's generation counter. The role is private fixture material, not logged.
func (f *Fixture) Sessions(ctx context.Context, t *testing.T) []functionmigration.Session {
	t.Helper()
	rows, err := f.Admin.Query(ctx, `SELECT a.pid,a.backend_start,a.application_name,s.ssl FROM pg_stat_activity a JOIN pg_stat_ssl s USING(pid) WHERE a.usename=$1 ORDER BY a.pid`, f.Role)
	if err != nil {
		t.Fatal("read native SQL backend identities")
	}
	defer rows.Close()
	var sessions []functionmigration.Session
	for rows.Next() {
		var s functionmigration.Session
		var started time.Time
		var application string
		if err := rows.Scan(&s.PID, &started, &application, &s.TLS); err != nil {
			t.Fatal("decode native SQL backend identity")
		}
		pid, err := strconv.Atoi(strings.TrimPrefix(application, "gregale-function-"))
		if err != nil || pid <= 0 || !strings.HasPrefix(application, "gregale-function-") || !s.TLS {
			t.Fatal("native SQL backend lacks worker identity or TLS")
		}
		s.WorkerPID, s.StartedAt = pid, started.UTC().Format(time.RFC3339Nano)
		sessions = append(sessions, s)
	}
	if rows.Err() != nil {
		t.Fatal("read native SQL backend identities")
	}
	return sessions
}
