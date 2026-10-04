// adr: 583
package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemRuntimeScalingStateIsolation(t *testing.T) {
	testRuntimeScalingStateIsolation(t, state.NewMemStore())
}

func testRuntimeScalingStateIsolation(t *testing.T, store runtimeAppEnvTestStore) {
	f := seedRuntimeAppEnv(t, store)
	ctx := t.Context()
	read := func(scope string) state.RuntimeScalingState {
		t.Helper()
		row, err := store.RuntimeScalingStateForDeployment(ctx, f.account.ID, f.app.ID, f.deployments[scope].ID)
		if err != nil || row.AccountID != f.account.ID || row.AppID != f.app.ID || row.DeploymentID != f.deployments[scope].ID || row.Scope != scope || row.EnvironmentID == "" {
			t.Fatalf("read %s: %+v %v", scope, row, err)
		}
		return row
	}
	if err := store.StampAppScaleIn(ctx, f.app.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.StampAppScaleOut(ctx, f.app.ID); err != nil {
		t.Fatal(err)
	}
	production := read("production")
	if production.LastScaleInAt == nil || production.LastScaleOutAt == nil {
		t.Fatal("production compatibility history missing")
	}
	for _, scope := range []string{"stage", "other"} {
		row := read(scope)
		if row.LastScaleInAt != nil || row.LastScaleOutAt != nil {
			t.Fatalf("%s inherited production clocks: %+v", scope, row)
		}
	}
	if err := store.StampDeploymentScaleOut(ctx, f.deployments["stage"].ID); err != nil {
		t.Fatal(err)
	}
	stage := read("stage")
	if stage.LastScaleOutAt == nil || stage.LastScaleInAt != nil {
		t.Fatalf("stage scale-out clock missing: %+v", stage)
	}
	if err := store.StampDeploymentScaleIn(ctx, f.deployments["stage"].ID); err != nil {
		t.Fatal(err)
	}
	staged := read("stage")
	if staged.LastScaleInAt == nil || !sameScalingTime(staged.LastScaleOutAt, stage.LastScaleOutAt) {
		t.Fatalf("stage scale-in replaced scale-out: %+v", staged)
	}
	// Timestamp pointers returned to a caller never alias persisted history.
	*staged.LastScaleInAt = time.Time{}
	if got := read("stage"); got.LastScaleInAt == nil || got.LastScaleInAt.IsZero() {
		t.Fatalf("caller changed history: %+v", got)
	}
	for _, scope := range []string{"production", "default"} {
		row := read(scope)
		if !sameScalingTime(row.LastScaleInAt, production.LastScaleInAt) || !sameScalingTime(row.LastScaleOutAt, production.LastScaleOutAt) {
			t.Fatalf("stage changed %s clocks: %+v", scope, row)
		}
	}
	app, err := store.AppByID(ctx, f.app.ID)
	if err != nil || !sameScalingTime(app.LastScaleInAt, production.LastScaleInAt) || !sameScalingTime(app.LastScaleOutAt, production.LastScaleOutAt) {
		t.Fatalf("stage changed App clocks: %+v %v", app, err)
	}
	// A new generation shares its environment's history, even after a desired
	// config edit. Its sibling stage starts with independent history.
	head, err := store.ProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "stage", f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	settings := head.Settings
	settings.IdleTimeoutS++
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "stage", f.app.ID, head.Revision, settings); err != nil {
		t.Fatal(err)
	}
	newDeployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "stage", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	newHistory, err := store.RuntimeScalingStateForDeployment(ctx, f.account.ID, f.app.ID, newDeployment.ID)
	oldHistory := read("stage")
	if err != nil || newHistory.EnvironmentID != oldHistory.EnvironmentID || !sameScalingTime(newHistory.LastScaleInAt, oldHistory.LastScaleInAt) || !sameScalingTime(newHistory.LastScaleOutAt, oldHistory.LastScaleOutAt) {
		t.Fatalf("generation lost environment history: %+v %v", newHistory, err)
	}
	if got := read("other"); got.LastScaleInAt != nil || got.LastScaleOutAt != nil {
		t.Fatalf("sibling history changed: %+v", got)
	}
	if err := store.StampDeploymentScaleOut(ctx, f.deployments["default"].ID); err != nil {
		t.Fatal(err)
	}
	if !sameScalingTime(read("default").LastScaleOutAt, read("production").LastScaleOutAt) {
		t.Fatal("default and production clocks split")
	}
	for _, stamp := range []func() error{
		func() error { return store.StampAppScaleIn(ctx, f.app.ID) },
		func() error { return store.StampAppScaleOut(ctx, f.app.ID) },
	} {
		if err := stamp(); err != nil {
			t.Fatal(err)
		}
	}
	app, err = store.AppByID(ctx, f.app.ID)
	row := read("production")
	if err != nil || !sameScalingTime(row.LastScaleInAt, app.LastScaleInAt) || !sameScalingTime(row.LastScaleOutAt, app.LastScaleOutAt) || !sameScalingTime(read("stage").LastScaleInAt, oldHistory.LastScaleInAt) || !sameScalingTime(read("stage").LastScaleOutAt, oldHistory.LastScaleOutAt) {
		t.Fatalf("compatibility stamps reached stage or missed production: %+v %v", row, err)
	}
	for _, ids := range [][3]string{{uuid.NewString(), f.app.ID, newDeployment.ID}, {f.account.ID, uuid.NewString(), newDeployment.ID}, {f.account.ID, f.app.ID, uuid.NewString()}} {
		if _, err := store.RuntimeScalingStateForDeployment(ctx, ids[0], ids[1], ids[2]); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("foreign owner accepted: %v", err)
		}
	}
	for _, bad := range []string{"invalid", uuid.Nil.String()} {
		if _, err := store.RuntimeScalingStateForDeployment(ctx, f.account.ID, f.app.ID, bad); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("invalid read: %v", err)
		}
		if err := store.StampDeploymentScaleIn(ctx, bad); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("invalid stamp: %v", err)
		}
	}
	for _, status := range []state.DeploymentStatus{state.DeployFailed, state.DeployCancelled} {
		if err := store.UpdateDeploymentStatus(ctx, newDeployment.ID, status, "test"); err != nil {
			t.Fatal(err)
		}
		if err := store.StampDeploymentScaleOut(ctx, newDeployment.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("terminal deployment wrote history: %v", err)
		}
	}
}

func sameScalingTime(a, b *time.Time) bool {
	return a == nil && b == nil || a != nil && b != nil && a.Equal(*b)
}

func TestMemRuntimeScalingStateLifetime(t *testing.T) {
	testRuntimeScalingStateLifetime(t, state.NewMemStore())
}

func testRuntimeScalingStateLifetime(t *testing.T, store runtimeAppEnvTestStore) {
	f := seedRuntimeAppEnv(t, store)
	ctx := t.Context()
	dep := f.deployments["stage"]
	if err := store.StampDeploymentScaleIn(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentSuperseded(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProjectEnvironment(ctx, f.account.ID, f.project.ID, "stage"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "stage"}); err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(f.app)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "stage", f.app.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	replacement, err := store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "stage", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RuntimeScalingStateForDeployment(ctx, f.account.ID, f.app.ID, dep.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("old lifetime read clocks: %v", err)
	}
	for _, stamp := range []func() error{func() error { return store.StampDeploymentScaleIn(ctx, dep.ID) }, func() error { return store.StampDeploymentScaleOut(ctx, dep.ID) }} {
		if err := stamp(); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("old lifetime stamped replacement: %v", err)
		}
	}
	for _, id := range []string{replacement.ID, f.deployments["production"].ID} {
		row, err := store.RuntimeScalingStateForDeployment(ctx, f.account.ID, f.app.ID, id)
		if err != nil || row.LastScaleInAt != nil || row.LastScaleOutAt != nil {
			t.Fatalf("replacement or production inherited old history: %+v %v", row, err)
		}
	}
}

func TestMemRuntimeScalingStateLegacyProduction(t *testing.T) {
	testRuntimeScalingStateLegacyProduction(t, state.NewMemStore())
}

func testRuntimeScalingStateLegacyProduction(t *testing.T, store runtimeAppEnvTestStore) {
	f := seedRuntimeAppEnv(t, store)
	ctx := t.Context()
	app, err := store.CreateApp(ctx, state.App{AccountID: f.account.ID, ProjectID: f.project.ID, WorkloadName: "legacy-clock", Slug: "legacy-clock", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	var rows []state.Deployment
	for _, scope := range []string{"default", "production", "stage"} {
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindImage})
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, dep)
	}
	if err := store.StampDeploymentScaleOut(ctx, rows[0].ID); err != nil {
		t.Fatal(err)
	}
	var stamp *time.Time
	for i, dep := range rows {
		row, err := store.RuntimeScalingStateForDeployment(ctx, f.account.ID, app.ID, dep.ID)
		env, envErr := store.RuntimeAppEnvForDeployment(ctx, f.account.ID, app.ID, dep.ID)
		values, valuesErr := store.RuntimeAppValuesForDeployment(ctx, f.account.ID, app.ID, dep.ID)
		if err != nil || envErr != nil || valuesErr != nil || row.EnvironmentID != env.EnvironmentID || row.EnvironmentID != values.EnvironmentID {
			t.Fatalf("legacy owner mismatch: %+v %+v %+v %v %v %v", row, env, values, err, envErr, valuesErr)
		}
		if i < 2 {
			if row.EnvironmentID != "" || row.LastScaleOutAt == nil || i == 1 && !sameScalingTime(stamp, row.LastScaleOutAt) {
				t.Fatalf("legacy production aliases split: %+v", row)
			}
			stamp = row.LastScaleOutAt
		} else if row.EnvironmentID == "" || row.LastScaleOutAt != nil {
			t.Fatalf("legacy stage adopted production history: %+v", row)
		}
	}
}

func TestMemRuntimeScalingStateMixedProductionGenerations(t *testing.T) {
	testRuntimeScalingStateMixedProductionGenerations(t, state.NewMemStore())
}

func testRuntimeScalingStateMixedProductionGenerations(t *testing.T, store runtimeAppEnvTestStore) {
	f := seedRuntimeAppEnv(t, store)
	ctx := t.Context()
	app, err := store.CreateApp(ctx, state.App{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "mixed-clock", WorkloadName: "mixed-clock", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "default", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.StampDeploymentScaleOut(ctx, legacy.ID); err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "production", app.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	pinned, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "stage", app.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	stage, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "stage", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.StampDeploymentScaleIn(ctx, stage.ID); err != nil {
		t.Fatal(err)
	}
	stageHistory, err := store.RuntimeScalingStateForDeployment(ctx, f.account.ID, app.ID, stage.ID)
	if err != nil {
		t.Fatal(err)
	}
	var priorIn, priorOut *time.Time
	for _, step := range []struct {
		deployment string
		in         bool
	}{{pinned.ID, true}, {legacy.ID, false}, {legacy.ID, true}, {pinned.ID, false}} {
		if step.in {
			err = store.StampDeploymentScaleIn(ctx, step.deployment)
		} else {
			err = store.StampDeploymentScaleOut(ctx, step.deployment)
		}
		if err != nil {
			t.Fatal(err)
		}
		first, err := store.RuntimeScalingStateForDeployment(ctx, f.account.ID, app.ID, legacy.ID)
		if err != nil {
			t.Fatal(err)
		}
		second, err := store.RuntimeScalingStateForDeployment(ctx, f.account.ID, app.ID, pinned.ID)
		if err != nil || first.EnvironmentID != "" || second.EnvironmentID == "" || first.LastScaleInAt == nil || first.LastScaleOutAt == nil || !sameScalingTime(first.LastScaleInAt, second.LastScaleInAt) || !sameScalingTime(first.LastScaleOutAt, second.LastScaleOutAt) {
			t.Fatalf("mixed production clocks diverged: legacy=%+v pinned=%+v %v", first, second, err)
		}
		if step.in && priorOut != nil && !sameScalingTime(first.LastScaleOutAt, priorOut) || !step.in && priorIn != nil && !sameScalingTime(first.LastScaleInAt, priorIn) {
			t.Fatal("mixed production stamp replaced the other direction")
		}
		priorIn, priorOut = first.LastScaleInAt, first.LastScaleOutAt
		staged, err := store.RuntimeScalingStateForDeployment(ctx, f.account.ID, app.ID, stage.ID)
		if err != nil || !sameScalingTime(staged.LastScaleInAt, stageHistory.LastScaleInAt) || staged.LastScaleOutAt != nil {
			t.Fatalf("production synchronization reached stage: %+v %v", staged, err)
		}
	}
}
