//go:build !no_pg

// adr: 592
package sched

// adr: 435, 581. PostgreSQL acceptance retains the same honest portable boundary.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresApplicationStandardComposedWaves(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	exerciseApplicationStandardComposedWaves(t, state.NewPgStore(pool), func(f *composedWaveFixture) {
		loop := NewLoop(pool, f.engine, f.engine.log)
		f.refresh = func(t *testing.T, request state.ApplicationStandardRuntimeRefreshRequest) {
			t.Helper()
			before, err := db.GetRuntimeConfigRestartStatus(t.Context(), pool, request.AppID, request.WakeID)
			if err != nil {
				t.Fatal("installed projection did not enqueue runtime work", err)
			}
			// Advance only the queue's test replay eligibility; consumer evidence,
			// authority leases and storage qualification clocks remain untouched.
			if _, err := pool.Exec(t.Context(), `UPDATE notification_outbox SET available_at=clock_timestamp() WHERE channel=$1 AND payload::jsonb->>'app_id'=$2 AND payload::jsonb->>'wake_id'=$3 AND state='pending'`, db.NotifyRuntimeConfigRestart, request.AppID, request.WakeID); err != nil {
				t.Fatal(err)
			}
			seen := 0
			_, err = db.DrainNotificationOutboxOnceForNode(t.Context(), pool, "composed-wave-schedd", f.vmm.identity.NodeID, []string{db.NotifyRuntimeConfigRestart}, func(ctx context.Context, n db.Notification) error {
				var actual state.ApplicationStandardRuntimeRefreshRequest
				if err := json.Unmarshal([]byte(n.Payload), &actual); err != nil {
					return err
				}
				if actual.AppID == request.AppID && actual.WakeID == request.WakeID {
					if actual != request || n.OutboxID == 0 {
						t.Fatal("outbox lost the installed revision binding", actual)
					}
					seen++
				}
				return loop.handleRuntimeConfigRestart(ctx, n)
			}, nil)
			if err != nil {
				t.Fatal("outbox delivery", err)
			}
			after, err := db.GetRuntimeConfigRestartStatus(t.Context(), pool, request.AppID, request.WakeID)
			wantSeen := 1
			if before.State == "delivered" {
				wantSeen = 0
			}
			if err != nil || seen != wantSeen || after.State != "delivered" || after.Attempts != 1 || after.CompletedAt == nil {
				t.Fatal("durable replacement was not settled exactly once", before, after, seen, err)
			}
		}
	})
}
