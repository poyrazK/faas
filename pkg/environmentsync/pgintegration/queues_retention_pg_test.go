package pgintegration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPGEnvironmentGitOpsQueueRecoveryRollbackAndLeaseGuards(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store, source, desired, app, original := queueIntentFixture(t, state.NewPgStore(pool), "enforce")
	adoptQueueIntent(t, store, source)
	queueIntentWorker(t, store)
	source = approveQueueRetirement(t, store, source, desired)
	queueIntentWorker(t, store)
	d := desired.Definition
	w := d.Workloads["api"]
	w.QueueRecoveries = map[string]string{"orders": original.Binding.ID}
	w.Variables["MODE"] = "recovered"
	d.Workloads["api"] = w
	source = approveQueueDefinition(t, store, source, d, "c")
	if _, err := pool.Exec(t.Context(), `update queue_bindings set retired_at=null where id=$1`, original.Binding.ID); err == nil {
		t.Fatal("approved metadata alone released a retirement hold")
	}
	adoptQueueIntent(t, store, source)
	assertQueueHeld(t, store, source, app, original.Binding.ID, original.Changes[0].TriggerID)
	lease, err := store.ClaimEnvironmentGitOps(t.Context(), uuid.NewString(), time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", "not-an-issued-lease"} {
		tx, err := pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(t.Context(), `select set_config('gregale.gitops_lease',$1,true)`, token); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(t.Context(), `update queue_bindings set retired_at=null where id=$1`, original.Binding.ID); err == nil {
			t.Fatal("unissued token recovered a queue")
		}
		_ = tx.Rollback(context.Background())
	}
	// Issued authority may release the hold, but cannot move its identity or
	// update intent in that same guard transition.
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `select set_config('gregale.gitops_lease',$1,true)`, lease.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `update queue_bindings set retired_at=null,queue_name='replacement' where id=$1`, original.Binding.ID); err == nil {
		t.Fatal("recovery guard accepted an identity/intent replacement")
	}
	_ = tx.Rollback(context.Background())
	compiled, err := environmentsync.Compile(d)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := store.ObserveEnvironmentGitOps(t.Context(), lease, compiled)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := environmentsync.BuildPlan(compiled, observed.State, observed.Owners, environmentsync.PlanOptions{Manager: source.ID, Revision: lease.Revision.ID, Generation: lease.Source.Generation, CommitSHA: lease.Revision.CommitSHA, Prune: lease.Source.Spec.Prune, Now: time.Now(), Overrides: observed.Overrides})
	if err != nil || !plan.CanApply() {
		t.Fatalf("recovery plan: %+v %v", plan, err)
	}
	if _, err := pool.Exec(t.Context(), `create function reject_recovered_consumer() returns trigger language plpgsql as $$
 begin if NEW.enabled and not OLD.enabled then raise exception 'forced consumer recovery failure'; end if; return NEW; end $$;
 create trigger reject_recovered_consumer before update of enabled on triggers for each row execute function reject_recovered_consumer()`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyEnvironmentGitOps(t.Context(), lease, plan); err == nil {
		t.Fatal("forced projection failure did not abort recovery")
	}
	assertQueueHeld(t, store, source, app, original.Binding.ID, original.Changes[0].TriggerID)
	assertVariable(t, store, source, app, "production", "MODE", "retired")
	// A new approved generation removes the old lease's authority even while
	// its physical deadline is still in the future.
	w = d.Workloads["api"]
	w.Variables["MODE"] = "new-generation"
	d.Workloads["api"] = w
	source = approveQueueDefinition(t, store, source, d, "d")
	tx, err = pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `select set_config('gregale.gitops_lease',$1,true)`, lease.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `update queue_bindings set retired_at=null where id=$1`, original.Binding.ID); err == nil {
		t.Fatal("superseded generation recovered retained work")
	}
	_ = tx.Rollback(context.Background())
	if _, err := pool.Exec(t.Context(), `drop trigger reject_recovered_consumer on triggers`); err != nil {
		t.Fatal(err)
	}
	queueIntentWorker(t, store)
	if row, err := store.QueueBindingByID(t.Context(), source.AccountID, app.ID, original.Binding.ID); err != nil || !row.Enabled || row.RetiredAt != nil {
		t.Fatalf("fresh controller did not recover original binding: %+v %v", row, err)
	}
	assertVariable(t, store, source, app, "production", "MODE", "new-generation")
}

func TestPGEnvironmentGitOpsQueueRetentionReplayPreservesHeldWorkAndLease(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store, source, desired, app, original := queueIntentFixture(t, state.NewPgStore(pool), "enforce")
	adoptQueueIntent(t, store, source)
	queueIntentWorker(t, store)
	consumerID := original.Changes[0].TriggerID
	work, err := store.EnqueueInvocation(t.Context(), state.Invocation{AccountID: source.AccountID, AppID: app.ID, Source: state.InvocationQueue, QueueName: "orders-v2", QueueBindingID: original.Binding.ID, DeploymentScope: "production", DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	receiptID, err := store.InsertTriggerRecord(t.Context(), consumerID, work.ID, []byte(`{}`), []byte(`{}`), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	source = approveQueueRetirement(t, store, source, desired)
	queueIntentWorker(t, store)
	before, err := store.QueueBindingHistoryByID(t.Context(), source.AccountID, app.ID, original.Binding.ID)
	if err != nil || before.RetiredAt == nil {
		t.Fatalf("retirement checkpoint: %+v %v", before, err)
	}
	d := desired.Definition
	w := d.Workloads["api"]
	w.QueueRecoveries = map[string]string{"orders": original.Binding.ID}
	d.Workloads["api"] = w
	source = approveQueueDefinition(t, store, source, d, "c")
	adoptQueueIntent(t, store, source)
	lease, err := store.ClaimEnvironmentGitOps(t.Context(), uuid.NewString(), time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// Replay the full GitOps additive set, including intervals where older
	// migrations replace the retirement, scope and managed-identity guards.
	versions := []int64{
		20260930183000001, 20260930183000002, 20260930183000003, 20260930183000004, 20260930183000005, 20260930183000006,
		20260930193000001, 20260930220000001,
		20261001010000001, 20261001020000001, 20261001020000002, 20261001030000001, 20261001040000001, 20261001050000001,
		20261001060000001, 20261001070000001, 20261001070000002, 20261001080000001, 20261001080000002, 20261001080000003, 20261001081007501,
		20261001094704872, 20261001110831601, 20261001120000001, 20261001142049282, 20261001143949543,
		20261001150000001, 20261001160000001, 20261001164005579, 20261001181539580, 20261001214705000, 20261001225012000,
	}
	if _, err := pool.Exec(t.Context(), `delete from goose_db_version where version_id=any($1::bigint[])`, versions); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	assertQueueHeld(t, store, source, app, original.Binding.ID, consumerID)
	after, err := store.QueueBindingHistoryByID(t.Context(), source.AccountID, app.ID, original.Binding.ID)
	if err != nil || !before.RetiredAt.Equal(*after.RetiredAt) {
		t.Fatalf("replay reset retirement evidence: %+v %v", after, err)
	}
	if err := store.RenewEnvironmentGitOps(t.Context(), lease, time.Now(), time.Minute); err != nil {
		t.Fatalf("replay lost the current lease: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `update queue_bindings set retired_at=null where id=$1`, original.Binding.ID); err == nil {
		t.Fatal("replayed guard granted unleased recovery")
	}
	compiled, err := environmentsync.Compile(d)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := store.ObserveEnvironmentGitOps(t.Context(), lease, compiled)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := environmentsync.BuildPlan(compiled, observed.State, observed.Owners, environmentsync.PlanOptions{Manager: source.ID, Revision: lease.Revision.ID, Generation: lease.Source.Generation, CommitSHA: lease.Revision.CommitSHA, Prune: lease.Source.Spec.Prune, Now: time.Now(), Overrides: observed.Overrides})
	if err != nil || !plan.CanApply() {
		t.Fatalf("replayed recovery plan: %+v %v", plan, err)
	}
	if _, err := store.ApplyEnvironmentGitOps(t.Context(), lease, plan); err != nil {
		t.Fatalf("replay lost approved recovery authority: %v", err)
	}
	if inv, err := store.InvocationByID(t.Context(), work.ID); err != nil || inv.QueueBindingID != original.Binding.ID || inv.State != state.InvocationPending {
		t.Fatalf("replay/recovery changed accepted work: %+v %v", inv, err)
	}
	if id, err := store.TriggerRecordIDByItemIdentifier(t.Context(), consumerID, work.ID); err != nil || id != receiptID {
		t.Fatalf("replay/recovery lost the original receipt: %s %v", id, err)
	}
}
