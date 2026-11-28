package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261104120000 — a stage's alert rule belongs to one of its app's
// project environments, keeps it, runs only on the stage's own signals, may
// share a production rule's name, and
// counts only the stage's failed invocations.
func TestPgAlertRuleEnvironments(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	suffix := uuid.NewString()[:8]
	account, err := store.CreateAccount(ctx, "alert-env-"+suffix+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "alert-env-" + suffix})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "alert-env-" + suffix,
		Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 128, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	stage, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "alert-env-other-" + suffix})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: other.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}

	rule := pgSampleAlertRule(account.ID, app.ID)
	rule.EnvironmentID = stage.ID
	created, err := store.CreateAlertRule(ctx, rule)
	if err != nil || created.EnvironmentID != stage.ID {
		t.Fatalf("stage rule = %+v, %v", created, err)
	}
	read, err := store.AlertRuleByID(ctx, created.ID)
	if err != nil || read.EnvironmentID != stage.ID {
		t.Fatalf("read back = %+v, %v", read, err)
	}

	checkViolation := func(name string, err error) {
		t.Helper()
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Fatalf("%s: err = %v, want a check violation", name, err)
		}
	}
	// Another project's environment, a moved rule and an enabled rule that
	// could not run in a stage are all refused.
	misplaced := pgSampleAlertRule(account.ID, app.ID)
	misplaced.EnvironmentID = foreign.ID
	_, err = store.CreateAlertRule(ctx, misplaced)
	checkViolation("foreign environment", err)
	_, err = pool.Exec(ctx, `update alert_rules set environment_id = null where id = $1::uuid`, created.ID)
	checkViolation("moved rule", err)
	rollback := pgSampleAlertRule(account.ID, app.ID)
	rollback.EnvironmentID, rollback.Action = stage.ID, state.AlertAction("rollback")
	_, err = store.CreateAlertRule(ctx, rollback)
	checkViolation("enabled rollback rule", err)
	rollback.Enabled = false
	if _, err := store.CreateAlertRule(ctx, rollback); err != nil {
		t.Fatalf("paused copy of a production-only rule: %v", err)
	}

	// Names are unique per app and environment: production may use the
	// stage rule's name, but not twice.
	sameName := pgSampleAlertRule(account.ID, app.ID)
	sameName.Name = created.Name
	if _, err := store.CreateAlertRule(ctx, sameName); err != nil {
		t.Fatalf("production rule with the stage rule's name: %v", err)
	}
	if _, err := store.CreateAlertRule(ctx, sameName); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("second production rule with that name: err = %v, want conflict", err)
	}

	// Failed invocations: one production, two of the stage's.
	now := time.Now().UTC()
	for _, environmentID := range []string{"", stage.ID, stage.ID} {
		var env any
		if environmentID != "" {
			env = environmentID
		}
		if _, err := pool.Exec(ctx, `insert into invocations (account_id, app_id, source, path, state, due_at, created_at, completed_at, deployment_scope, environment_id)
			values ($1::uuid, $2::uuid, 'cron', '/run', 'failed', $3, $3, $3, '', $4::uuid)`, account.ID, app.ID, now.Add(-time.Minute), env); err != nil {
			t.Fatal(err)
		}
	}
	n, err := store.CountFailedInvocationsInEnvironmentSince(ctx, account.ID, app.ID, stage.ID, "", now.Add(-time.Hour))
	if err != nil || n != 2 {
		t.Fatalf("stage failures = %d, %v; want 2", n, err)
	}
	if n, err := store.CountFailedInvocationsInEnvironmentSince(ctx, account.ID, app.ID, stage.ID, state.InvocationQueue, now.Add(-time.Hour)); err != nil || n != 0 {
		t.Fatalf("stage queue failures = %d, %v; want 0", n, err)
	}
	// Production's rules count only production's failure, app or account wide.
	for _, appID := range []string{app.ID, ""} {
		if n, err := store.CountFailedProductionInvocationsSince(ctx, account.ID, appID, "", now.Add(-time.Hour)); err != nil || n != 1 {
			t.Fatalf("production failures (app %q) = %d, %v; want 1", appID, n, err)
		}
	}

	// Deleting the stage removes its rules.
	if _, err := pool.Exec(ctx, `delete from invocations where environment_id = $1::uuid`, stage.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `delete from project_environments where id = $1::uuid`, stage.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AlertRuleByID(ctx, created.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stage rule after the stage's deletion: err = %v, want not found", err)
	}
}
