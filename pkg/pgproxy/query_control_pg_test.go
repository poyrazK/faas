//go:build !no_pg

package pgproxy

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbquerycontrol"
)

func TestOperatorQueryCancelFencesReusedPooledBackend(t *testing.T) {
	h := newHarnessWith(t, func(c *Config) { c.PoolMaxServers = 1 })
	h.waker.epoch.Store(time.Now().UnixMicro())
	h.proxy.queryControls.Store(true)
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	databaseID := uuid.NewString()
	role, password := h.addRole(t, databaseID)
	pooled := databaseID + pooledSuffix + "." + testSuffix
	open := func(host, application string) *pgx.Conn {
		t.Helper()
		conn, err := h.connect(ctx, host, role, password, h.dbName, "require", map[string]string{"application_name": application})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close(context.WithoutCancel(ctx)) })
		return conn
	}
	first := open(pooled, "orders-api")
	second := open(pooled, "orders-worker")
	unrelated := open(databaseID+"."+testSuffix, "orders-direct")
	const lock = 364715
	if _, err := h.backend.Exec(ctx, "SELECT pg_advisory_lock($1)", lock); err != nil {
		t.Fatal(err)
	}
	startBlocked := func(conn *pgx.Conn) <-chan error {
		result := make(chan error, 1)
		go func() {
			_, err := conn.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", lock)
			result <- err
		}()
		return result
	}
	waitTarget := func() dbquerycontrol.Target {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
			active := h.proxy.cancellableQueries()
			if len(active) == 1 {
				target := dbquerycontrol.FromProto(active[0])
				var blocked bool
				if err := h.backend.QueryRow(ctx, `SELECT EXISTS (
					SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event_type='Lock'
				)`, target.BackendPID).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					return target
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("no tracked blocked query")
		return dbquerycontrol.Target{}
	}

	other := make(chan error, 1)
	go func() {
		_, err := unrelated.Exec(ctx, "SELECT pg_sleep(1)")
		other <- err
	}()
	done := startBlocked(first)
	old := waitTarget()
	if old.ApplicationName != "orders-api" || old.DatabaseID != databaseID {
		t.Fatalf("target owner: %+v", old)
	}
	results, err := h.backend.PgConn().Exec(ctx, dbguest.SessionSQL).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, result := range results {
		if len(result.Rows) == 0 {
			continue
		}
		var sample dbguest.SessionSample
		if err := json.Unmarshal(result.Rows[0][0], &sample); err != nil {
			t.Fatal(err)
		}
		for _, row := range sample.Sessions {
			if row.PID == old.BackendPID && row.State == "active" && row.WaitEventType == "Lock" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("session SQL omitted the blocked backend")
	}
	if outcome := h.proxy.cancelObservedQuery(ctx, old); outcome != dbquerycontrol.SignalSent {
		t.Fatalf("first cancel: %s", outcome)
	}
	canceledWithin(t, done, "operator query cancel")
	if err := <-other; err != nil {
		t.Fatalf("unrelated query was interrupted: %v", err)
	}
	var one int
	if err := unrelated.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatalf("unrelated session: %d, %v", one, err)
	}

	done = startBlocked(second)
	current := waitTarget()
	if current.BackendPID != old.BackendPID || current.ID == old.ID || current.ApplicationName != "orders-worker" {
		t.Fatalf("pooled backend was not reused by the second client: old=%+v current=%+v", old, current)
	}
	if outcome := h.proxy.cancelObservedQuery(ctx, old); outcome != dbquerycontrol.Stale {
		t.Fatalf("old cancel: %s", outcome)
	}
	if _, err := h.backend.Exec(ctx, "SELECT pg_advisory_unlock($1)", lock); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("stale target canceled subsequent work: %v", err)
	}
}
