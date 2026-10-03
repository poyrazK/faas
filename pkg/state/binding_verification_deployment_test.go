package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBindingVerificationDeploymentMem(t *testing.T) {
	bindingVerificationDeploymentSuite(t, state.NewMemStore())
}

func TestBindingVerificationDeploymentPG(t *testing.T) {
	store, _ := pgStore(t)
	bindingVerificationDeploymentSuite(t, store)
}

func bindingVerificationDeploymentSuite(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@target.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "target-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	createDeployment := func(scope string, traffic int) state.Deployment {
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, Scope: scope, TrafficPercent: traffic, TrafficPercentExplicit: true, ImageDigest: "sha256:" + strings.Repeat("1", 64), CreatedAt: time.Now().UTC()})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, dep.ID, "/test/"+dep.ID, "test/"+dep.ID, 4096); err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeployLive, ""); err != nil {
			t.Fatal(err)
		}
		return dep
	}
	a, b, scoped := createDeployment("default", 100), createDeployment("default", 0), createDeployment("staging", 0)
	tasks, evidence := store.(state.AppTaskStore), store.(state.BindingVerificationStore)
	now := time.Now().UTC().Truncate(time.Microsecond)
	params := state.CreateAppTaskParams{AccountID: account.ID, AppID: app.ID, Kind: state.AppTaskKindManual, Command: []string{api.AppTaskPostgresBindingProbeCommand, "DATABASE_URL"}, MaxOutputBytes: 4096, TimeoutSeconds: 15, RequireLiveDeployment: true, BindingVerification: &state.BindingVerificationPin{Type: api.BindingTypePostgres, Binding: "DATABASE_URL", Revision: strings.Repeat("a", 64)}}
	admit := func(dep state.Deployment, at time.Time) state.AppTask {
		params.DeploymentID, params.CreatedAt = dep.ID, at
		task, err := tasks.CreateAppTask(ctx, params)
		if err != nil {
			t.Fatal(err)
		}
		if task.DeploymentID != dep.ID || task.ArtifactKey != "test/"+dep.ID || task.DeploymentScope != dep.Scope {
			t.Fatalf("wrong pinned task: %+v", task)
		}
		return task
	}
	old := admit(a, now)
	admit(b, now.Add(time.Second))
	latestA := admit(a, now.Add(2*time.Second))
	admit(scoped, now.Add(3*time.Second))
	// Completing an earlier task cannot replace the newest admission on A.
	running, err := tasks.ClaimNextAppTask(ctx, "target-test", now.Add(4*time.Second), time.Minute)
	if err != nil || running.ID != old.ID {
		t.Fatalf("claim: %+v %v", running, err)
	}
	running, err = tasks.MarkAppTaskRunning(ctx, running.ID, *running.LeaseToken, now.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	zero := 0
	if _, err := tasks.CompleteAppTask(ctx, state.CompleteAppTaskParams{ID: running.ID, LeaseToken: *running.LeaseToken, Status: state.AppTaskSucceeded, ExitCode: &zero, FinishedAt: now.Add(6 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	for _, dep := range []state.Deployment{a, b, scoped} {
		rows, err := evidence.ListBindingVerificationTasks(ctx, account.ID, app.ID, []string{api.BindingTypePostgres}, state.BindingVerificationSelection{DeploymentID: dep.ID})
		if err != nil || len(rows) != 1 || rows[0].DeploymentID != dep.ID || rows[0].Scope != dep.Scope || rows[0].Status != "queued" {
			t.Fatalf("exact %s: %+v %v", dep.ID, rows, err)
		}
		if dep.ID == a.ID && !rows[0].CreatedAt.Equal(latestA.CreatedAt) {
			t.Fatalf("late completion replaced A: %+v", rows)
		}
	}
	missing := uuid.NewString()
	rows, err := evidence.ListBindingVerificationTasks(ctx, account.ID, app.ID, []string{api.BindingTypePostgres}, state.BindingVerificationSelection{DeploymentID: missing})
	if err != nil || len(rows) != 0 {
		t.Fatalf("explicit missing fell back: %+v %v", rows, err)
	}
	// Default inventory prefers A's own admission even after a newer B probe.
	admit(b, now.Add(7*time.Second))
	rows, err = evidence.ListBindingVerificationTasks(ctx, account.ID, app.ID, []string{api.BindingTypePostgres}, state.BindingVerificationSelection{DeploymentID: a.ID, AllowFallback: true})
	if err != nil || len(rows) != 2 || rows[0].DeploymentID != a.ID {
		t.Fatalf("default lost selected evidence: %+v %v", rows, err)
	}
	rows, err = evidence.ListBindingVerificationTasks(ctx, account.ID, app.ID, []string{api.BindingTypePostgres}, state.BindingVerificationSelection{DeploymentID: missing, AllowFallback: true})
	if err != nil || len(rows) != 2 || rows[0].DeploymentID != b.ID {
		t.Fatalf("legacy stale fallback lost: %+v %v", rows, err)
	}
	if err := store.UpdateDeploymentStatus(ctx, b.ID, state.DeploySuperseded, ""); err != nil {
		t.Fatal(err)
	}
	params.DeploymentID = b.ID
	if _, err := tasks.CreateAppTask(ctx, params); !errors.Is(err, state.ErrAppTaskDeploymentUnavailable) {
		t.Fatalf("superseded explicit target admitted: %v", err)
	}
	params.RequireLiveDeployment = false
	if _, err := tasks.CreateAppTask(ctx, params); err != nil {
		t.Fatalf("legacy pinned admission changed: %v", err)
	}
	params.RequireLiveDeployment = true
	params.BindingVerification = nil
	if _, err := tasks.CreateAppTask(ctx, params); !errors.Is(err, state.ErrAppTaskInvalid) {
		t.Fatalf("unmanaged explicit task admitted: %v", err)
	}
}

func TestBindingVerificationDeploymentPGWaitsForConcurrentRetirement(t *testing.T) {
	store, pool, _ := pgStoreWithPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@target-race.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "target-race-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:" + strings.Repeat("1", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, dep.ID, "/test/rootfs", "test/rootfs", 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	writer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback(ctx) }()
	if _, err := writer.Exec(ctx, `UPDATE deployments SET status='superseded' WHERE id=$1`, dep.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := store.CreateAppTask(ctx, state.CreateAppTaskParams{AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Kind: state.AppTaskKindManual, Command: []string{api.AppTaskPostgresBindingProbeCommand, "DATABASE_URL"}, RequireLiveDeployment: true, BindingVerification: &state.BindingVerificationPin{Type: api.BindingTypePostgres, Binding: "DATABASE_URL", Revision: strings.Repeat("a", 64)}})
		done <- err
	}()
	// Confirm the actual query is blocked by the writer, then commit retirement.
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%name: CreateBindingVerificationTask%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("admission bypassed deployment writer: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("admission never reached deployment lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, state.ErrAppTaskDeploymentUnavailable) {
			t.Fatalf("retired target admitted after lock wait: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM app_tasks WHERE app_id=$1`, app.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("task persisted after retirement: %d %v", count, err)
	}
}
