package state_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// AdvanceEventExecutionReconciliations sets a requested subscription's ledger
// baseline only when its app has no execution the ledger cannot attribute; a
// subscription nobody asked to reconcile keeps its unknown baseline.
func TestAdvanceEventExecutionReconciliationsPG(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	cleanAccount, clean := seedPgApp(t, s, t.Context())
	blockedAccount, blocked := seedPgApp(t, s, t.Context())

	coverage := func(app state.App, account state.Account, requested bool) string {
		t.Helper()
		sub := uuid.NewString()
		if _, err := pool.Exec(t.Context(), `
			INSERT INTO event_execution_coverage(subscription_id,account_id,app_id,started_at,baseline_known,ledger_started_at,ledger_baseline_known,reconciliation_requested_at)
			VALUES($1,$2,$3,clock_timestamp(),false,clock_timestamp(),false,CASE WHEN $4 THEN clock_timestamp() END)`,
			sub, account.ID, app.ID, requested); err != nil {
			t.Fatal(err)
		}
		return sub
	}
	ready := coverage(clean, cleanAccount, true)
	unrequested := coverage(clean, cleanAccount, false)
	held := coverage(blocked, blockedAccount, true)
	// An execution with no event_execution_roots row cannot be attributed to
	// a subscription, so it holds back the blocked app's reconciliation.
	if _, err := pool.Exec(t.Context(), `INSERT INTO invocations (app_id,account_id,source,queue_name,deployment_scope) VALUES ($1,$2,'queue','unattributed','default')`,
		blocked.ID, blockedAccount.ID); err != nil {
		t.Fatal(err)
	}

	n, err := s.AdvanceEventExecutionReconciliations(t.Context(), api.EventExecutionReconciliationBatch)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("advanced %d reconciliations, want 1", n)
	}
	for sub, want := range map[string]bool{ready: true, unrequested: false, held: false} {
		var known bool
		if err := pool.QueryRow(t.Context(), `SELECT ledger_baseline_known FROM event_execution_coverage WHERE subscription_id=$1`, sub).Scan(&known); err != nil {
			t.Fatal(err)
		}
		if known != want {
			t.Errorf("subscription %s ledger_baseline_known = %v, want %v", sub, known, want)
		}
	}

	// A second pass finds nothing left to advance.
	if n, err := s.AdvanceEventExecutionReconciliations(t.Context(), api.EventExecutionReconciliationBatch); err != nil || n != 0 {
		t.Fatalf("second pass advanced %d (err %v), want 0", n, err)
	}
}
