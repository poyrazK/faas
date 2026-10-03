// adr: 521 — environment intent and runtime ownership contracts.
package sched

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// PostgreSQL intent/demand and the real scheduler through a VM transport
// fixture. Native guest execution and teardown remain a separate acceptance.
func TestWorkerScopedDemandPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	testWorkerScopedDemandLifecycle(t, state.NewPgStore(pool))
}
