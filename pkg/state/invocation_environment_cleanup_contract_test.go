// adr: 531
package state_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemInvocationEnvironmentOwner(t *testing.T) {
	testInvocationEnvironmentOwner(t, state.NewMemStore())
}

func testInvocationEnvironmentOwner(t *testing.T, store invocationWorkEnvironmentTestStore) {
	t.Helper()
	f := seedInvocationWorkEnvironment(t, store)
	ctx := t.Context()
	stage, err := store.EnqueueInvocation(ctx, f.request(t.Context(), t, store, "staging"))
	if err != nil {
		t.Fatal(err)
	}
	env, err := store.ProjectEnvironmentBySlug(ctx, f.account.ID, f.project.ID, "staging")
	if err != nil || stage.EnvironmentID != env.ID {
		t.Fatalf("stage owner = %q, environment = %+v, %v", stage.EnvironmentID, env, err)
	}
	stored, err := store.InvocationByID(ctx, stage.ID)
	if err != nil || stored.EnvironmentID != env.ID {
		t.Fatalf("stored owner = %q, %v", stored.EnvironmentID, err)
	}
	encoded, err := json.Marshal(stored)
	if err != nil || bytes.Contains(encoded, []byte("environment_id")) {
		t.Fatalf("private owner leaked in invocation response: %s, %v", encoded, err)
	}
	for _, fault := range []string{"missing_pin", "missing_marker", "missing_both", "foreign_marker", "production_pin"} {
		t.Run(fault, func(t *testing.T) {
			changed := stored
			switch fault {
			case "missing_pin":
				changed.Headers = json.RawMessage(`{}`)
			case "missing_marker":
				changed.EnvironmentID = ""
			case "missing_both":
				changed.EnvironmentID, changed.Headers = "", json.RawMessage(`{}`)
			case "foreign_marker":
				changed.EnvironmentID = uuid.NewString()
			case "production_pin":
				prod := f.request(ctx, t, store, "production")
				changed.Headers = prod.Headers
			}
			if _, _, err := state.ResolveInvocationVersion(ctx, store, changed); !errors.Is(err, state.ErrInvocationEnvironmentWorkIsolation) {
				t.Fatalf("damaged stored stage selected a deployment: %v", err)
			}
		})
	}
	prod, err := store.EnqueueInvocation(ctx, f.request(t.Context(), t, store, "production"))
	if err != nil || prod.EnvironmentID != "" {
		t.Fatalf("production ownership changed: %+v, %v", prod, err)
	}
	if _, err := store.ClaimInvocation(ctx, stage.ID, "", 30); err != nil {
		t.Fatalf("ordinary stage claim: %v", err)
	}
}

func TestMemInvocationEnvironmentCleanupDrainsIdleWork(t *testing.T) {
	testInvocationEnvironmentCleanupDrainsIdleWork(t, state.NewMemStore())
}

func testInvocationEnvironmentCleanupDrainsIdleWork(t *testing.T, store invocationWorkEnvironmentTestStore) invocationWorkEnvironmentFixture {
	t.Helper()
	f := seedInvocationWorkEnvironment(t, store)
	ctx := t.Context()
	var stageRows []state.Invocation
	for _, scope := range []string{"production", "empty-stage", "staging"} {
		for n := 0; n < 2; n++ {
			row, err := store.EnqueueKeyedInvocation(ctx, f.request(t.Context(), t, store, scope), f.latest, "s:same")
			if err != nil {
				t.Fatalf("idle cleanup setup: %v", err)
			}
			if scope == "staging" {
				stageRows = append(stageRows, row)
			}
		}
	}
	plain, err := store.EnqueueInvocation(ctx, f.request(t.Context(), t, store, "staging"))
	if err != nil {
		t.Fatalf("idle cleanup setup: %v", err)
	}
	finished, err := store.EnqueueKeyedInvocation(ctx, f.request(t.Context(), t, store, "staging"), f.serial, "s:finished", "s:customer")
	if err != nil {
		t.Fatalf("idle cleanup setup: %v", err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, finished.ID, "", 30, 10); err != nil {
		t.Fatalf("claim finished: %v", err)
	}
	if err := store.CompleteKeyedInvocation(ctx, finished.ID, 1, nil); err != nil {
		t.Fatalf("complete finished: %v", err)
	}
	stageRows = append(stageRows, plain, finished)
	retireInvocationEnvironmentDeployments(t, store, f, "staging")
	if err := store.DeleteProjectEnvironment(ctx, f.account.ID, f.project.ID, "staging"); err != nil {
		t.Fatalf("delete retired stage: %v", err)
	}
	for _, row := range stageRows {
		if _, err := store.InvocationByID(ctx, row.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("owned invocation remains after delete: %s, %v", row.ID, err)
		}
		if _, err := store.InvocationWorkEnvironmentAdmission(ctx, row.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("owned admission remains after delete: %s, %v", row.ID, err)
		}
	}
	for _, scope := range []string{"production", "empty-stage"} {
		row, err := store.EnqueueKeyedInvocation(ctx, f.request(t.Context(), t, store, scope), f.latest, "s:same")
		if err != nil || row.WorkSequence != 3 {
			t.Fatalf("%s lane altered by stage cleanup: sequence=%d, %v", scope, row.WorkSequence, err)
		}
		if _, err := store.ClaimInvocationWithCap(ctx, row.ID, "", 30, 10); err != nil {
			t.Fatalf("%s work cannot run after sibling cleanup: %v", scope, err)
		}
	}
	return f
}

func TestMemInvocationEnvironmentCleanupWaitsForRunningWork(t *testing.T) {
	testInvocationEnvironmentCleanupWaitsForRunningWork(t, state.NewMemStore())
}

func testInvocationEnvironmentCleanupWaitsForRunningWork(t *testing.T, store invocationWorkEnvironmentTestStore) {
	t.Helper()
	f := seedInvocationWorkEnvironment(t, store)
	ctx := t.Context()
	row, err := store.EnqueueKeyedInvocation(ctx, f.request(t.Context(), t, store, "staging"), f.serial, "s:running", "s:customer")
	if err != nil {
		t.Fatal(err)
	}
	// The lease is already expired, but the reaper has not released capacity.
	if _, err := store.ClaimInvocationWithCap(ctx, row.ID, "", -1, 10); err != nil {
		t.Fatal(err)
	}
	retireInvocationEnvironmentDeployments(t, store, f, "staging")
	if err := store.DeleteProjectEnvironment(ctx, f.account.ID, f.project.ID, "staging"); !errors.Is(err, state.ErrEnvironmentInvocationWorkBusy) {
		t.Fatalf("running stage removed: %v", err)
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, f.account.ID, f.project.ID, "staging"); err != nil {
		t.Fatalf("busy deletion left partial environment: %v", err)
	}
	if actual, err := store.InvocationByID(ctx, row.ID); err != nil || actual.State != state.InvocationDispatching || !actual.QuotaReserved {
		t.Fatalf("busy deletion changed execution/capacity: %+v, %v", actual, err)
	}
	if count, err := store.RequeueExpiredInvocations(ctx, time.Now(), 10); err != nil || count != 1 {
		t.Fatalf("release expired stage execution: count=%d, %v", count, err)
	}
	if actual, err := store.InvocationByID(ctx, row.ID); err != nil || actual.State != state.InvocationPending || actual.QuotaReserved {
		t.Fatalf("recovery retained execution/capacity: %+v, %v", actual, err)
	}
	if err := store.DeleteProjectEnvironment(ctx, f.account.ID, f.project.ID, "staging"); err != nil {
		t.Fatalf("recovered stage cannot be removed: %v", err)
	}
}

func retireInvocationEnvironmentDeployments(t *testing.T, store invocationEnvironmentTestStore, f invocationWorkEnvironmentFixture, scope string) {
	t.Helper()
	rows, err := store.ListDeploymentsForApp(t.Context(), f.app.ID, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, dep := range rows {
		if dep.Scope == scope && dep.Status == state.DeployLive {
			if err := store.UpdateDeploymentStatus(t.Context(), dep.ID, state.DeploySuperseded, ""); err != nil {
				t.Fatal(err)
			}
		}
	}
}
