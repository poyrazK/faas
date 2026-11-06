// adr: 20261104110000
// adr: 20261104201200000
package state_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemProjectEnvironmentLifecycle(t *testing.T) {
	t.Run("clone membership", func(t *testing.T) { testEnvironmentCloneMembership(t, state.NewMemStore()) })
	t.Run("legacy clone admission", func(t *testing.T) { testEnvironmentLegacyCloneAdmission(t, state.NewMemStore()) })
	t.Run("cleanup reservation", func(t *testing.T) { testEnvironmentCleanupReservation(t, state.NewMemStore()) })
	t.Run("retired rollback", func(t *testing.T) { testEnvironmentRetiredRollback(t, state.NewMemStore()) })
	t.Run("rollback deletion race", func(t *testing.T) { testEnvironmentRollbackDeletionRace(t, state.NewMemStore()) })
	t.Run("last stage membership", func(t *testing.T) { testEnvironmentLastStageMembership(t, state.NewMemStore()) })
}

func testEnvironmentLastStageMembership(t *testing.T, store state.Store) {
	ctx := t.Context()
	account, project, production := environmentLifecycleFixture(t, store)
	lifecycleEnvironment(t, store, account, project, "qa")
	stage, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "lifecycle-stage-only", WorkloadName: "experimental", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(stage)
	if err != nil {
		t.Fatal(err)
	}
	specs := store.(state.ProjectEnvironmentWorkloadSpecStore)
	if _, err := specs.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "qa", stage.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	// No deployment history is needed to preserve the service's stage origin.
	if err := store.DeleteProjectEnvironment(ctx, account.ID, project.ID, "qa"); err != nil {
		t.Fatal(err)
	}
	reader := store.(state.ProjectEnvironmentAppReader)
	ids, err := reader.ProjectEnvironmentAppIDs(ctx, account.ID, project.ID, "production")
	if err != nil || len(ids) != 1 || ids[0] != production.ID {
		t.Fatalf("deleting the last stage admitted its service to production: %v %v", ids, err)
	}
	preserved, err := state.EnvironmentOnlyProjectApps(ctx, store, account.ID, project.ID)
	if err != nil || !preserved[stage.ID] {
		t.Fatalf("stage origin lost after deletion: %v %v", preserved, err)
	}
	lifecycleEnvironment(t, store, account, project, "qa")
	ids, err = reader.ProjectEnvironmentAppIDs(ctx, account.ID, project.ID, "qa")
	if err != nil || len(ids) != 0 {
		t.Fatalf("recreated environment inherited membership: %v %v", ids, err)
	}
	if _, err := specs.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "production", stage.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	ids, err = reader.ProjectEnvironmentAppIDs(ctx, account.ID, project.ID, "production")
	if err != nil || len(ids) != 2 {
		t.Fatalf("explicit production admission was refused: %v %v", ids, err)
	}
}

func environmentLifecycleFixture(t *testing.T, store state.Store) (state.Account, state.Project, state.App) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "lifecycle"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "lifecycle-web", WorkloadName: "web", Type: state.AppTypeApp, RAMMB: 256, StartCommand: "serve-production"})
	if err != nil {
		t.Fatal(err)
	}
	return account, project, app
}

func lifecycleEnvironment(t *testing.T, store state.Store, account state.Account, project state.Project, slug string) state.ProjectEnvironment {
	t.Helper()
	env, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: slug})
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func lifecycleLiveDeployment(t *testing.T, store state.Store, app state.App, scope string) state.Deployment {
	t.Helper()
	ctx := t.Context()
	d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindTarball, ImageDigest: "sha256:lifecycle"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, d.ID, "/rootfs/"+d.ID, "apps/"+d.ID, 42); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	return d
}

func testEnvironmentCloneMembership(t *testing.T, store state.Store) {
	ctx := t.Context()
	account, project, app := environmentLifecycleFixture(t, store)
	manifest := app.Manifest
	manifest.RevisionPinTTLSeconds = 3600
	app, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest})
	if err != nil {
		t.Fatal(err)
	}
	specs := store.(state.ProjectEnvironmentWorkloadSpecStore)
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	settings.RAMMB, settings.StartCommand = 512, "serve-source"
	if _, err := specs.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "production", app.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	d := lifecycleLiveDeployment(t, store, app, "production")
	if _, err := store.(state.ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 300, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: d.ID}}); err != nil {
		t.Fatal(err)
	}
	lifecycleEnvironment(t, store, account, project, "qa")
	extra, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "lifecycle-experiment", WorkloadName: "experiment", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	extraSettings, err := state.WorkloadSettingsFromApp(extra)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := specs.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "qa", extra.ID, 0, extraSettings); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppEnvInScope(ctx, account.ID, app.ID, "production", "SOURCE_ONLY", "production"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppEnvInScope(ctx, account.ID, extra.ID, "qa", "QA_ONLY", "qa"); err != nil {
		t.Fatal(err)
	}
	cloner := store.(interface {
		CloneProjectEnvironment(context.Context, state.ProjectEnvironmentClone, api.Limits) (state.ProjectEnvironment, state.ProjectEnvironmentCloneResult, error)
	})
	capture := store.(state.ProjectEnvironmentCloneValuesStore)
	for _, source := range []string{"production", "qa"} {
		snapshot, err := capture.CaptureProjectEnvironmentCloneValues(ctx, account.ID, project.ID, source)
		if err != nil || len(snapshot.ValueScopes) != 1 {
			t.Fatalf("%s capture: %+v %v", source, snapshot, err)
		}
		target := source + "-copy"
		_, result, err := cloner.CloneProjectEnvironment(ctx, state.ProjectEnvironmentClone{AccountID: account.ID, ProjectID: project.ID, SourceSlug: source, TargetSlug: target, ExpectedSourceValueScopes: snapshot.ValueScopes, ExpectedSourceValuesHash: snapshot.Hash}, api.MustLimitsFor(account.Plan))
		if err != nil || result.WorkloadsCopied != 1 {
			t.Fatalf("%s clone: %+v %v", source, result, err)
		}
		wanted, omitted := app, extra
		if source == "qa" {
			wanted, omitted = extra, app
		}
		if _, err := specs.ProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, target, wanted.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := specs.ProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, target, omitted.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("cloned unrelated app into %s: %v", target, err)
		}
		vars, err := store.ListAppEnvInScope(ctx, account.ID, wanted.ID, target)
		if err != nil || len(vars) != 1 {
			t.Fatalf("cloned variables: %+v %v", vars, err)
		}
		ids, err := store.(state.ProjectEnvironmentAppReader).ProjectEnvironmentAppIDs(ctx, account.ID, project.ID, target)
		if err != nil || len(ids) != 1 || ids[0] != wanted.ID {
			t.Fatalf("target membership: %v %v", ids, err)
		}
	}
	snapshot, err := capture.CaptureProjectEnvironmentCloneValues(ctx, account.ID, project.ID, "qa")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := specs.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "qa", app.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	_, _, err = cloner.CloneProjectEnvironment(ctx, state.ProjectEnvironmentClone{AccountID: account.ID, ProjectID: project.ID, SourceSlug: "qa", TargetSlug: "stale-copy", ExpectedSourceValueScopes: snapshot.ValueScopes, ExpectedSourceValuesHash: snapshot.Hash}, api.MustLimitsFor(account.Plan))
	if !errors.Is(err, state.ErrConflict) {
		t.Fatalf("accepted stale source membership: %v", err)
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, account.ID, project.ID, "stale-copy"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("partial clone remains: %v", err)
	}
}

func testEnvironmentLegacyCloneAdmission(t *testing.T, store state.Store) {
	t.Helper()
	ctx := t.Context()
	account, project, app := environmentLifecycleFixture(t, store)
	cloner := store.(interface {
		CloneProjectEnvironment(context.Context, state.ProjectEnvironmentClone, api.Limits) (state.ProjectEnvironment, state.ProjectEnvironmentCloneResult, error)
	})
	for i := range 2 {
		_, result, err := cloner.CloneProjectEnvironment(ctx, state.ProjectEnvironmentClone{AccountID: account.ID, ProjectID: project.ID, SourceSlug: "production", TargetSlug: fmt.Sprintf("copy-%d", i)}, api.MustLimitsFor(account.Plan))
		if err != nil || result.WorkloadsCopied != 1 {
			t.Fatalf("legacy production clone %d: %+v %v", i, result, err)
		}
		ids, err := store.(state.ProjectEnvironmentAppReader).ProjectEnvironmentAppIDs(ctx, account.ID, project.ID, "production")
		if err != nil || len(ids) != 1 || ids[0] != app.ID {
			t.Fatalf("clone changed production membership: %v %v", ids, err)
		}
	}
	spec, err := store.(state.ProjectEnvironmentWorkloadSpecStore).ProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "production", app.ID)
	if err != nil || spec.Settings.RAMMB != app.RAMMB || spec.Settings.StartCommand != app.StartCommand {
		t.Fatalf("clone changed legacy production settings: %+v %v", spec, err)
	}
}

func testEnvironmentCleanupReservation(t *testing.T, store state.Store) {
	t.Helper()
	ctx := t.Context()
	account, project, app := environmentLifecycleFixture(t, store)
	lifecycleEnvironment(t, store, account, project, "staging")
	cleanup := store.(state.ProjectEnvironmentCleanupStore)
	bindingID := uuid.NewString()
	resources := state.ProjectEnvironmentCleanupResources{Postgres: []state.ProjectEnvironmentPostgresCleanupResource{{AppID: app.ID, Scope: "staging", BindingID: bindingID, DatabaseID: uuid.NewString()}}}
	job, err := cleanup.DeleteProjectEnvironmentWithCleanup(ctx, account.ID, project.ID, "staging", resources, "request", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	assertBlocked := func() {
		t.Helper()
		if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("recreated environment before cleanup: %v", err)
		}
		_, _, err := store.(interface {
			CloneProjectEnvironment(context.Context, state.ProjectEnvironmentClone, api.Limits) (state.ProjectEnvironment, state.ProjectEnvironmentCloneResult, error)
		}).CloneProjectEnvironment(ctx, state.ProjectEnvironmentClone{AccountID: account.ID, ProjectID: project.ID, SourceSlug: "production", TargetSlug: "staging"}, api.MustLimitsFor(account.Plan))
		if !errors.Is(err, state.ErrConflict) {
			t.Fatalf("cloned environment before cleanup: %v", err)
		}
	}
	assertBlocked()
	claimed, err := cleanup.ClaimNextProjectEnvironmentCleanup(ctx, "worker", job.LeaseUntil.Add(time.Second), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	assertBlocked()
	if err := cleanup.RetryProjectEnvironmentCleanup(ctx, claimed.ID, claimed.LeaseToken, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	assertBlocked()
	claimed, err = cleanup.ClaimNextProjectEnvironmentCleanup(ctx, "worker-2", time.Now().Add(2*time.Minute), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanup.CompleteProjectEnvironmentCleanup(ctx, claimed.ID, claimed.LeaseToken); err != nil {
		t.Fatal(err)
	}
	lifecycleEnvironment(t, store, account, project, "staging")
}

func testEnvironmentRetiredRollback(t *testing.T, store state.Store) {
	t.Helper()
	ctx := t.Context()
	account, project, app := environmentLifecycleFixture(t, store)
	old := lifecycleEnvironment(t, store, account, project, "staging")
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	settings.RAMMB, settings.StartCommand = 512, "serve-staging"
	if _, err := store.(state.ProjectEnvironmentWorkloadSpecStore).PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "staging", app.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	d := lifecycleLiveDeployment(t, store, app, "staging")
	newer := lifecycleLiveDeployment(t, store, app, "staging")
	if err := store.UpdateDeploymentStatus(ctx, newer.ID, state.DeployFailed, "stopped"); err != nil {
		t.Fatal(err)
	}
	production := lifecycleLiveDeployment(t, store, app, "production")
	if err := store.DeleteProjectEnvironment(ctx, account.ID, project.ID, "staging"); err != nil {
		t.Fatal(err)
	}
	recreated := lifecycleEnvironment(t, store, account, project, "staging")
	if old.ID == recreated.ID {
		t.Fatal("reused environment identity")
	}
	history, err := store.DeploymentByID(ctx, d.ID)
	if err != nil || history.Status != state.DeploySuperseded || history.DeletedAt == nil {
		t.Fatalf("lost retired audit history: %+v %v", history, err)
	}
	if _, err := store.GetDeploymentByIDScopedToSuperseded(ctx, app.ID, d.ID); !errors.Is(err, state.ErrNoRollbackTarget) {
		t.Fatalf("selected old rollback: %v", err)
	}
	if _, err := store.PrepareDeploymentRollback(ctx, app.ID, d.ID); !errors.Is(err, state.ErrNoRollbackTarget) {
		t.Fatalf("prepared old rollback: %v", err)
	}
	if _, err := store.LatestSupersededDeployment(ctx, app.ID, "staging"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("implicit old rollback: %v", err)
	}
	if err := store.MarkDeploymentLive(ctx, d.ID); err == nil {
		t.Fatal("activated retired deployment")
	}
	if err := store.UpdateDeploymentStatus(ctx, d.ID, state.DeployLive, ""); err == nil {
		t.Fatal("revived retired deployment by status mutation")
	}
	if _, err := state.AppForDeployment(ctx, store, history); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("resolved retired release with fallback settings: %v", err)
	}
	ids, err := store.(state.ProjectEnvironmentAppReader).ProjectEnvironmentAppIDs(ctx, account.ID, project.ID, "staging")
	if err != nil || len(ids) != 0 {
		t.Fatalf("inherited historical membership: %v %v", ids, err)
	}
	if dep, err := store.DeploymentByID(ctx, production.ID); err != nil || dep.Status != state.DeployLive || dep.DeletedAt != nil {
		t.Fatalf("retired another environment: %+v %v", dep, err)
	}
	dark, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindTarball, ImageDigest: "sha256:dark", TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, dark.ID, "/rootfs/"+dark.ID, "apps/"+dark.ID, 42); err != nil {
		t.Fatal(err)
	}
	if err := store.(state.ProjectPromotionDeploymentStore).MarkDeploymentLiveDark(ctx, dark.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(ctx, newer.ID, state.DeployFailed, "late failure from deleted environment"); err != nil {
		t.Fatal(err)
	}
	if current, err := store.DeploymentByID(ctx, dark.ID); err != nil || current.Status != state.DeployLive || current.TrafficPercent != 0 {
		t.Fatalf("late old failure promoted the replacement environment's dark release: %+v %v", current, err)
	}
	// The recreated environment remains usable for newly built releases.
	fresh := lifecycleLiveDeployment(t, store, app, "staging")
	if _, err := store.(state.CheckedRollbackStore).CreateCheckedRollback(ctx, account.ID, app.ID, d.ID, fresh.ID, "retired target"); err == nil {
		t.Fatal("prepared a checked rollback to the previous environment's release")
	}
}

func testEnvironmentRollbackDeletionRace(t *testing.T, store state.Store) {
	t.Helper()
	ctx := t.Context()
	account, project, app := environmentLifecycleFixture(t, store)
	for i := range 5 {
		scope := fmt.Sprintf("race-%d", i)
		lifecycleEnvironment(t, store, account, project, scope)
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindTarball})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, d.ID, "/rootfs/"+d.ID, "apps/"+d.ID, 42); err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateDeploymentStatus(ctx, d.ID, state.DeploySuperseded, ""); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		prepared, deleted := make(chan error, 1), make(chan error, 1)
		go func() { <-start; _, err := store.PrepareDeploymentRollback(ctx, app.ID, d.ID); prepared <- err }()
		go func() { <-start; deleted <- store.DeleteProjectEnvironment(ctx, account.ID, project.ID, scope) }()
		close(start)
		prepareErr, deleteErr := <-prepared, <-deleted
		if (prepareErr == nil) == (deleteErr == nil) {
			t.Fatalf("expected exactly one winner: prepare=%v delete=%v", prepareErr, deleteErr)
		}
		if deleteErr == nil {
			lifecycleEnvironment(t, store, account, project, scope)
			if err := store.MarkDeploymentLive(ctx, d.ID); err == nil {
				t.Fatal("revived race loser in recreated stage")
			}
		} else {
			if _, err := store.ProjectEnvironmentBySlug(ctx, account.ID, project.ID, scope); err != nil {
				t.Fatalf("lost prepared rollback environment: %v", err)
			}
		}
	}
}
