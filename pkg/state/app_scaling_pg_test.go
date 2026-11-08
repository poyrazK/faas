//go:build !no_pg

// adr: 20261104104521
package state_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgRecoverableAppScaling(t *testing.T) {
	store, _ := pgStore(t)
	testRecoverableAppScaling(t, store)
}

func TestPgScalingReceiptFailureRollsBackSettingsAndRevision(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "scale-atomic-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "atomic-" + uuid.NewString()[:8], Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 4})
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.AppScaling(ctx, account.ID, app.ID, "default")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION reject_scaling_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected receipt failure'; END $$; CREATE TRIGGER reject_scaling_receipt BEFORE INSERT ON app_scaling_receipts FOR EACH ROW EXECUTE FUNCTION reject_scaling_receipt()`); err != nil {
		t.Fatal(err)
	}
	ram := 512
	change := state.AppScalingChange{AccountID: account.ID, AppID: app.ID, Scope: "default", Request: api.ApplyAppScalingRequest{ExpectedAppID: app.ID, RequestID: uuid.NewString(), ExpectedRevision: &before.Revision, RAMMB: &ram}}
	if _, err := store.ApplyAppScaling(ctx, change); err == nil {
		t.Fatal("receipt insert failure accepted")
	}
	after, err := store.AppScaling(ctx, account.ID, app.ID, "default")
	if err != nil || after != before {
		t.Fatalf("partial mutation after receipt failure: %+v %v", after, err)
	}
	if _, err := store.AppScalingReceipt(ctx, account.ID, app.ID, "default", change.Request.RequestID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("failed transaction has receipt: %v", err)
	}
	if _, err := pool.Exec(ctx, `DROP TRIGGER reject_scaling_receipt ON app_scaling_receipts`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyAppScaling(ctx, change); err != nil {
		t.Fatalf("original request cannot retry after rollback: %v", err)
	}
}

func TestPgScalingRecoveryDoesNotRequireCurrentWorkloadHead(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "scale-head-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "head-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "head-" + uuid.NewString()[:8], Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 4})
	if err != nil {
		t.Fatal(err)
	}
	env, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, env.Slug, app.ID, 0, settings)
	if err != nil {
		t.Fatal(err)
	}
	ram := 512
	change := state.AppScalingChange{AccountID: account.ID, AppID: app.ID, Scope: env.Slug, Request: api.ApplyAppScalingRequest{ExpectedAppID: app.ID, RequestID: uuid.NewString(), ExpectedEnvironmentID: env.ID, ExpectedRevision: &spec.Revision, RAMMB: &ram}}
	receipt, err := store.ApplyAppScaling(ctx, change)
	if err != nil {
		t.Fatal(err)
	}
	// Promotion rollback can remove a head that did not exist before promotion.
	if _, err := pool.Exec(ctx, `DELETE FROM project_environment_workload_heads WHERE environment_id=$1 AND app_id=$2`, env.ID, app.ID); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.AppScalingReceipt(ctx, account.ID, app.ID, env.Slug, change.Request.RequestID)
	if err != nil || !reflect.DeepEqual(recovered, receipt) {
		t.Fatalf("receipt depends on current head: %+v %v", recovered, err)
	}
	replayed, err := store.ApplyAppScaling(ctx, change)
	if err != nil || !reflect.DeepEqual(replayed, receipt) {
		t.Fatalf("replay depends on current head: %+v %v", replayed, err)
	}
}
