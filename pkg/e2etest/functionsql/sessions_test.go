package functionsql

// adr: 1577

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSessionsPostgres(t *testing.T) {
	if os.Getenv("FAAS_TEST_FUNCTION_POSTGRES") != "1" {
		t.Skip("set FAAS_TEST_FUNCTION_POSTGRES=1 with a disposable TLS/SCRAM cluster")
	}
	f := New(t)
	open := func() (*pgxpool.Pool, []*pgxpool.Conn) {
		cfg := f.Admin.Config().Copy()
		cfg.ConnConfig.User, cfg.ConnConfig.Password = f.Role, f.password
		cfg.ConnConfig.RuntimeParams["application_name"] = "gregale-function-1234"
		cfg.MaxConns, cfg.MinConns = 2, 0
		pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
		if err != nil {
			t.Fatal("open real SQL witness sessions")
		}
		t.Cleanup(pool.Close)
		var sessions []*pgxpool.Conn
		for range 2 {
			conn, err := pool.Acquire(t.Context())
			if err != nil {
				t.Fatal("acquire real SQL witness session")
			}
			t.Cleanup(conn.Release)
			sessions = append(sessions, conn)
		}
		return pool, sessions
	}
	beforePool, beforeConns := open()
	before := f.Sessions(t.Context(), t)
	if len(before) != 2 || before[0].WorkerPID != 1234 || before[1].WorkerPID != 1234 || before[0].PID == before[1].PID {
		t.Fatal("independent SQL witness did not identify both TLS sessions")
	}
	for _, conn := range beforeConns {
		conn.Release()
	}
	beforePool.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		if count, _ := f.Connections(ctx, t); count == 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("original SQL backends did not retire")
		case <-time.After(10 * time.Millisecond):
		}
	}
	_, _ = open()
	after := f.Sessions(t.Context(), t)
	if len(after) != 2 {
		t.Fatal("new SQL sessions were not observed")
	}
	for _, old := range before {
		for _, fresh := range after {
			if old.PID == fresh.PID && old.StartedAt == fresh.StartedAt {
				t.Fatal("reconnected pool reused an original SQL backend identity")
			}
		}
	}
}
