// adr: 191 — the owner claims and settles an explicit wake's durable outbox
// row; a peer leaves it pending without counting an attempt.
package sched_test

import (
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// enqueueExplicitWake writes the durable app_wake row apid writes and
// returns the delivery Loop.Run receives for it.
func enqueueExplicitWake(t *testing.T, pool *pgxpool.Pool, appID, wakeID string) db.Notification {
	t.Helper()
	n := explicitWake(appID, wakeID)
	if err := db.Notify(t.Context(), pool, n.Channel, n.Payload); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `select max(id) from notification_outbox where channel = $1`, n.Channel).Scan(&n.OutboxID); err != nil {
		t.Fatal(err)
	}
	return n
}

func outboxRow(t *testing.T, pool *pgxpool.Pool, id int64) (string, int) {
	t.Helper()
	var st string
	var attempts int
	if err := pool.QueryRow(t.Context(), `select state, attempts from notification_outbox where id = $1`, id).Scan(&st, &attempts); err != nil {
		t.Fatal(err)
	}
	return st, attempts
}

func TestExplicitAppWakeSettlesItsOutboxRow(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	f := newFailoverFleet(t, 2, 4096, func(string) error { return nil })
	loop := sched.NewLoop(pool, f.e, slog.New(slog.DiscardHandler))

	t.Run("owner delivers it", func(t *testing.T) {
		app := f.app(t, api.PlanPro, 256, 1)
		n := enqueueExplicitWake(t, pool, app.ID, "0198f89a-0000-7000-8000-000000000021")
		loop.XDispatchExplicitWake(t.Context(), n)
		loop.WaitPrimes()
		if st, attempts := outboxRow(t, pool, n.OutboxID); st != "delivered" || attempts != 1 {
			t.Fatalf("outbox row = %s after %d attempts, want delivered after 1", st, attempts)
		}
		if _, err := f.store.RunningInstanceForApp(t.Context(), app.ID); err != nil {
			t.Fatalf("running instance after the explicit wake: %v", err)
		}
	})
	t.Run("peer leaves it to the owner", func(t *testing.T) {
		app := f.app(t, api.PlanPro, 256, 1, func(a *state.App) { a.NodeID = f.nodes[1].ID })
		n := enqueueExplicitWake(t, pool, app.ID, "0198f89a-0000-7000-8000-000000000022")
		loop.XDispatchExplicitWake(t.Context(), n)
		loop.WaitPrimes()
		if st, attempts := outboxRow(t, pool, n.OutboxID); st != "pending" || attempts != 0 {
			t.Fatalf("outbox row = %s after %d attempts, want pending and unclaimed for the owner", st, attempts)
		}
	})
}
