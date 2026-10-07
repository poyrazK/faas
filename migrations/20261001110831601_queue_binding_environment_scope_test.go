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

func TestMigrationQueueBindingEnvironmentScopePreservesLegacyAndCapturedWork(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.Open(t)
	migrateUpTo(t, ctx, pool, 20261001094704872)
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "queue-scope-migration@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "queue-scope-migration"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "queue-scope-migration", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	legacy := seedLegacyQueueBindingConsumer(t, pool, account.ID, app.ID, "orders", true)
	var old string
	if err := pool.QueryRow(ctx, `insert into invocations(account_id,app_id,source,queue_name,deployment_scope,payload,due_at)
 values($1,$2,'queue','orders','production','{}',now()) returning id`, account.ID, app.ID).Scan(&old); err != nil {
		t.Fatal(err)
	}
	receipt, err := store.InsertTriggerRecord(ctx, legacy.Changes[0].TriggerID, old, []byte(`{}`), []byte(`{}`), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	historical, err := store.QueueBindingByID(ctx, account.ID, app.ID, legacy.Binding.ID)
	if err != nil || historical.DeploymentScope != "" {
		t.Fatalf("upgrade assigned old shared binding: %+v %v", historical, err)
	}
	row, err := store.InvocationByID(ctx, old)
	if err != nil || row.QueueBindingID != legacy.Binding.ID || row.DeploymentScope != "production" {
		t.Fatalf("upgrade changed accepted identity: %+v %v", row, err)
	}
	scoped, err := store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, DeploymentScope: "production", Name: "orders", QueueName: "orders", Mode: "push", Enabled: true, WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: account.ID, AppID: app.ID, Source: state.InvocationQueue, QueueName: "orders", DueAt: time.Now().Add(-time.Second)})
	if err != nil || fresh.QueueBindingID != scoped.Binding.ID {
		t.Fatalf("exact scope did not take future admission: %+v %v", fresh, err)
	}
	if _, err := store.ClaimQueueTriggerInvocation(ctx, old, scoped.Changes[0].TriggerID, app.ID, "orders", 60); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("new scope adopted old work: %v", err)
	}
	claimed, err := store.ClaimQueueTriggerInvocation(ctx, fresh.ID, scoped.Changes[0].TriggerID, app.ID, "orders", 60)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ query, id, constraint string }{
		{`update queue_bindings set deployment_scope='preview' where id=$1`, scoped.Binding.ID, "queue_binding_scope_identity"},
		{`update queue_bindings set environment_id=gen_random_uuid() where id=$1`, scoped.Binding.ID, "queue_binding_scope_identity"},
		{`update queue_bindings set deployment_scope='production' where id=$1`, legacy.Binding.ID, "queue_binding_scope_identity"},
		{`update triggers set queue_binding_scope='preview' where id=$1`, scoped.Changes[0].TriggerID, "queue_consumer_scope_identity"},
		{`update triggers set queue_binding_environment_id=gen_random_uuid() where id=$1`, scoped.Changes[0].TriggerID, "queue_consumer_scope_identity"},
		{`update invocations set deployment_scope='preview' where id=$1`, fresh.ID, "invocation_deployment_scope_identity"},
	} {
		_, err := pool.Exec(ctx, item.query, item.id)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.ConstraintName != item.constraint {
			t.Fatalf("scope identity guard=%v want=%s", err, item.constraint)
		}
	}
	if _, err := pool.Exec(ctx, `delete from goose_db_version where version_id=20261001110831601`); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	current, err := store.InvocationByID(ctx, fresh.ID)
	if err != nil || current.QueueBindingID != scoped.Binding.ID || current.DeploymentScope != claimed.DeploymentScope || current.Attempts != claimed.Attempts || current.State != claimed.State || current.LeaseExpiresAt == nil || !current.LeaseExpiresAt.Equal(*claimed.LeaseExpiresAt) {
		t.Fatalf("replay changed scoped claim: %+v %v", current, err)
	}
	trigger, err := store.TriggerByID(ctx, scoped.Changes[0].TriggerID)
	if err != nil || trigger.QueueBindingScope != "production" || trigger.QueueBindingEnvironmentID.String() != scoped.Binding.EnvironmentID {
		t.Fatalf("replay lost consumer scope: %+v %v", trigger, err)
	}
	if id, err := store.TriggerRecordIDByItemIdentifier(ctx, legacy.Changes[0].TriggerID, old); err != nil || id != receipt {
		t.Fatalf("migration changed legacy receipt: %q %v", id, err)
	}
}

func TestMigrationQueueBindingEnvironmentScopeRecreatedSlugCannotReleaseCapturedWork(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "queue-scope-recreation@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "queue-scope-recreation"})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "queue-scope-recreation", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	bind := func() state.QueueBindingConsumerResult {
		t.Helper()
		binding, err := store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, DeploymentScope: "staging", Name: "orders", QueueName: "orders", Mode: "push", Enabled: true, WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 1})
		if err != nil {
			t.Fatal(err)
		}
		return binding
	}
	enqueue := func(queue string) state.Invocation {
		t.Helper()
		inv, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: account.ID, AppID: app.ID, Source: state.InvocationQueue, DeploymentScope: "staging", QueueName: queue, DueAt: time.Now().Add(-time.Second)})
		if err != nil {
			t.Fatal(err)
		}
		return inv
	}
	original := bind()
	old := enqueue("orders")
	receipt, err := store.InsertTriggerRecord(ctx, original.Changes[0].TriggerID, old.ID, []byte(`{}`), []byte(`{}`), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProjectEnvironment(ctx, account.ID, project.ID, "staging"); err != nil {
		t.Fatal(err)
	}
	recreated, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil || recreated.ID == environment.ID {
		t.Fatalf("recreation reused environment identity: %+v %v", recreated, err)
	}
	replacement := bind()
	if original.Binding.EnvironmentID != environment.ID || replacement.Binding.EnvironmentID != recreated.ID {
		t.Fatal("binding failed to retain immutable catalog identity")
	}
	for _, queue := range []string{"orders", ""} {
		if inv := enqueue(queue); inv.QueueBindingID != replacement.Binding.ID {
			t.Fatalf("future admission selected unavailable original consumer: %+v", inv)
		}
	}
	if _, err := store.ClaimQueueTriggerInvocation(ctx, old.ID, replacement.Changes[0].TriggerID, app.ID, "orders", 60); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("replacement consumer inherited old work: %v", err)
	}
	// An out-of-order replay can reinstall the older admission and receipt
	// functions before this migration is replayed. Its independent guards
	// must continue to hold already captured work during that interval.
	if _, err := pool.Exec(ctx, `delete from goose_db_version where version_id in (20261001080000003,20261001081007501)`); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, old.ID, "", 60, 10); !errors.Is(err, state.ErrQueueBindingEnvironmentUnavailable) {
		t.Fatalf("older function replay released original environment: %v", err)
	}
	if rows, err := store.ClaimTriggerRecordsByItems(ctx, original.Changes[0].TriggerID, []string{old.ID}); err != nil || len(rows) != 0 {
		t.Fatalf("older function replay released original receipt: %+v %v", rows, err)
	}
	if _, err := pool.Exec(ctx, `delete from goose_db_version where version_id=20261001110831601`); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	row, err := store.QueueBindingHistoryByID(ctx, account.ID, app.ID, original.Binding.ID)
	if err != nil || row.EnvironmentID != environment.ID {
		t.Fatalf("replay reinterpreted old environment identity: %+v %v", row, err)
	}
	if id, err := store.TriggerRecordIDByItemIdentifier(ctx, original.Changes[0].TriggerID, old.ID); err != nil || id != receipt {
		t.Fatalf("replay lost old receipt: %q %v", id, err)
	}
	oldRow, err := store.InvocationByID(ctx, old.ID)
	if err != nil || oldRow.QueueBindingID != original.Binding.ID || oldRow.State != state.InvocationPending {
		t.Fatalf("held work changed: %+v %v", oldRow, err)
	}
}
