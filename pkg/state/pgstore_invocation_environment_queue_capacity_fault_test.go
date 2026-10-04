//go:build !no_pg

// adr: 583
package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgEnvironmentQueueProducerDepthRetainsDamagedOwnership(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	f := seedQueueConsumers(t, store)
	if _, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, f.dep.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountPlan(ctx, f.account.ID, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	limit := api.MustLimitsFor(api.PlanHobby).MaxQueueDepth
	var rows []state.Invocation
	for range limit {
		rows = append(rows, enqueueStageQueue(t.Context(), t, store, f))
	}
	// A damaged row marker must not erase authoritative admission ownership.
	if _, err := pool.Exec(ctx, `UPDATE invocations SET environment_id=NULL,source='async_invoke',queue_name='' WHERE id=$1`, rows[0].ID); err != nil {
		t.Fatal(err)
	}
	// A missing proof must not erase an invocation's persisted stage owner.
	if _, err := pool.Exec(ctx, `DELETE FROM invocation_environment_queue_admissions WHERE invocation_id=$1`, rows[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnqueueProjectEnvironmentQueueInvocation(ctx, f.account.ID, f.project.ID, f.dep.ID, "orders", state.Invocation{}); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("damaged ownership freed producer depth: %v", err)
	}
	var messages, proofs int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM invocations WHERE account_id=$1),(SELECT count(*) FROM invocation_environment_queue_admissions WHERE account_id=$1)`, f.account.ID).Scan(&messages, &proofs); err != nil || messages != limit || proofs != limit-1 {
		t.Fatalf("rejection changed ledger: %d %d %v", messages, proofs, err)
	}
	if _, _, err := store.GetAccountAsyncQuota(ctx, f.account.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("rejected producer reserved claim quota: %v", err)
	}
}
