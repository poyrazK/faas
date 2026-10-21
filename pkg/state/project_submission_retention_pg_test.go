//go:build !no_pg

package state_test

// adr: 20261021094500

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectSubmissionRetentionPostgres(t *testing.T) {
	store, _ := pgStore(t)
	testProjectSubmissionRetention(t, store)
}

// Attempt-keyed release state keeps its receipt past the retention cutoff.
func TestProjectSubmissionRetentionKeepsReferencedReceiptsPostgres(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "receipt-reference@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	ids := reserveRetentionReceipts(t, store, account.ID, "", "")
	if _, err := pool.Exec(ctx, `INSERT INTO project_release_config_intents(account_id,attempt_id,payload) VALUES($1,$2,'{}')`, account.ID, ids[1]); err != nil {
		t.Fatal(err)
	}
	if n, err := store.PruneProjectSubmissions(ctx, time.Now().Add(time.Hour), 10); err != nil || n != 1 {
		t.Fatalf("prune: %d %v", n, err)
	}
	if _, err := store.ProjectSubmission(ctx, account.ID, ids[1]); err != nil {
		t.Fatalf("referenced receipt pruned: %v", err)
	}
	var _ state.ProjectSubmissionRetentionStore = store
}
