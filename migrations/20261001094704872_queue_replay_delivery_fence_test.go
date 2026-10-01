//go:build !no_pg

package migrations_test

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMigrationQueueReplayDeliveryFenceRetainsHistory(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.Open(t)
	migrateUpTo(t, ctx, pool, 20261001081007501)
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "replay-fence-migration@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "replay-fence-migration", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, Name: "jobs", QueueName: "jobs", Mode: "push", Enabled: true, WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	var id string
	if err := pool.QueryRow(ctx, `insert into invocations(account_id,app_id,source,queue_name,deployment_scope,state,attempts,last_replayed_at,lease_expires_at,payload)
values($1,$2,'queue','jobs','staging','dispatching',1,now()-interval '1 hour',now()+interval '1 hour','{"job":"history"}') returning id`, account.ID, app.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	receipt, err := store.InsertTriggerRecord(ctx, binding.Changes[0].TriggerID, id, []byte(`{"job":"history"}`), []byte(`{}`), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update trigger_records set state='dead_letter',attempts=3,claim_generation=7 where id=$1`, receipt); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	row, err := store.InvocationByID(ctx, id)
	if err != nil || row.ReplayGeneration != 1 || row.State != state.InvocationDispatching || row.Attempts != 1 || row.QueueBindingID != binding.Binding.ID || row.DeploymentScope != "staging" {
		t.Fatalf("upgrade changed accepted work=%+v %v", row, err)
	}
	if _, err := state.AdmitPlatformTenantInvocation(ctx, store, app.ID, state.Invocation{ID: id, Source: "esm", Attempts: 1}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("pre-upgrade replay envelope admitted=%v", err)
	}
	_, err = pool.Exec(ctx, `update invocations set replay_generation=0 where id=$1`, id)
	var constraint *pgconn.PgError
	if !errors.As(err, &constraint) || constraint.ConstraintName != "invocation_replay_generation_identity" {
		t.Fatalf("generation reset allowed=%v", err)
	}
	if err := store.FailInvocation(ctx, id, "again", time.Nanosecond, 1); err != nil {
		t.Fatal(err)
	}
	row, err = store.RetryQueueDeadLetter(ctx, account.ID, id)
	if err != nil || row.ReplayGeneration != 2 || row.Attempts != 0 {
		t.Fatalf("new replay=%+v %v", row, err)
	}
	claims, err := store.ClaimTriggerRecordsByItems(ctx, binding.Changes[0].TriggerID, []string{id})
	if err != nil || len(claims) != 1 || claims[0].ID.String() != receipt || claims[0].ClaimGeneration != 9 {
		t.Fatalf("receipt identity/generation=%+v %v", claims, err)
	}
	if _, err := pool.Exec(ctx, `delete from goose_db_version where version_id=20261001094704872`); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	row, err = store.InvocationByID(ctx, id)
	if err != nil || row.ReplayGeneration != 2 || row.State != state.InvocationPending || row.Attempts != 0 {
		t.Fatalf("migration replay changed work=%+v %v", row, err)
	}
	var actualGeneration int64
	var actualExpiry time.Time
	if err := pool.QueryRow(ctx, `select claim_generation,claim_expires_at from trigger_records where id=$1`, receipt).Scan(&actualGeneration, &actualExpiry); err != nil || actualGeneration != 9 || !actualExpiry.Equal(claims[0].ClaimExpiresAt.Time) {
		t.Fatalf("migration replay stole receipt lease=%d %v %v", actualGeneration, actualExpiry, err)
	}
}
