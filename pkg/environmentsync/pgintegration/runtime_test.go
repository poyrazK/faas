package pgintegration_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func finishColdGitOpsRuntime(t *testing.T, basic gitOpsTestStore, lease state.EnvironmentGitOpsLease) {
	t.Helper()
	runtime := basic.(state.EnvironmentGitOpsRuntimeStore)
	pending, err := runtime.PendingEnvironmentGitOpsRuntime(t.Context(), lease)
	if err != nil {
		t.Fatal(err)
	}
	for _, effect := range pending {
		progress, err := runtime.ReconcileEnvironmentGitOpsRuntime(t.Context(), lease, effect.ID)
		if err != nil || !progress.Ready || progress.Request != nil {
			t.Fatalf("cold runtime did not converge without waking: %+v %v", progress, err)
		}
	}
}

func runtimeInstance(t *testing.T, store state.Store, app state.App, deployment state.Deployment, next state.State) state.Instance {
	t.Helper()
	node, err := store.CreateComputeNode(t.Context(), state.ComputeNode{Name: "gitops-" + uuid.NewString(), Active: true,
		TargetURL: "tcp://127.0.0.1:50051", AdmissionCeilingMB: 4096, MemMB: 8192, VPCPUs: 4, VCPUBudget: 160, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstance(t.Context(), app.ID, deployment.ID, string(state.StateWaking), app.RAMMB, node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if next != state.StateWaking {
		if err := store.UpdateInstanceState(t.Context(), instance.ID, string(next)); err != nil {
			t.Fatal(err)
		}
	}
	return instance
}

func runtimeFixture(t *testing.T, basic gitOpsTestStore, resident bool) (intentTestStore, state.EnvironmentGitOpsLease, environmentsync.DesiredState, state.App, state.Deployment, state.Instance) {
	t.Helper()
	store := basic.(intentTestStore)
	source, desired, app := intentFixture(t, store, "enforce")
	adoption, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, adoption.Hash); err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "production",
		Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("c", 64), Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	var instance state.Instance
	if resident {
		instance = runtimeInstance(t, store, app, deployment, state.StateRunning)
	}
	if _, err := store.CreateSnapshot(t.Context(), state.Snapshot{DeploymentID: deployment.ID, FCVersion: "1.13.0", StorageKey: state.SnapMemKey(deployment.ID)}); err != nil {
		t.Fatal(err)
	}
	lease, err := store.ClaimEnvironmentGitOps(t.Context(), "runtime-worker", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	plan := claimedIntentPlan(t, store, lease, desired)
	effects := basic.(state.EnvironmentGitOpsEffectStore)
	if _, err := effects.ApplyEnvironmentGitOpsWithEffects(t.Context(), lease, plan, []state.EnvironmentGitOpsEffectSpec{{AppID: app.ID,
		Kind: "edge_policy", GatewayGeneration: 42, MatchHosts: []string{"api.example.test"}}}); err != nil {
		t.Fatal(err)
	}
	pending, err := effects.PendingEnvironmentGitOpsEffects(t.Context(), lease)
	if err != nil {
		t.Fatal(err)
	}
	for _, effect := range pending {
		if err := effects.CompleteEnvironmentGitOpsEffect(t.Context(), lease, effect.ID); err != nil {
			t.Fatal(err)
		}
	}
	return store, lease, desired, app, deployment, instance
}

func runtimeFinish(t *testing.T, store intentTestStore, lease state.EnvironmentGitOpsLease, desired environmentsync.DesiredState) error {
	t.Helper()
	verified := claimedIntentPlan(t, store, lease, desired)
	raw, _ := json.Marshal(verified)
	now := time.Now()
	return store.FinishEnvironmentGitOps(t.Context(), lease, "converged", raw, json.RawMessage(`[]`), "", now, now.Add(time.Minute))
}

func TestEnvironmentGitOpsRuntimeRequiresFreshResidentsAndReadiness(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, lease, desired, app, deployment, old := runtimeFixture(t, basic, true)
		runtime := basic.(state.EnvironmentGitOpsRuntimeStore)
		pending, err := runtime.PendingEnvironmentGitOpsRuntime(t.Context(), lease)
		if err != nil || len(pending) != 1 || pending[0].AppID != app.ID || pending[0].Environment != "production" {
			t.Fatalf("intent commit did not retain runtime work: %+v %v", pending, err)
		}
		effect := pending[0]
		if !errors.Is(runtimeFinish(t, store, lease, desired), state.ErrConflict) {
			t.Fatal("database equality hid an old running process")
		}
		progress, err := runtime.ReconcileEnvironmentGitOpsRuntime(t.Context(), lease, effect.ID)
		if err != nil || progress.Ready {
			t.Fatalf("old process was accepted: %+v %v", progress, err)
		}
		if _, memory := basic.(*state.MemStore); memory && (progress.Request == nil || progress.Request.WakeID != effect.WakeID || progress.Request.Scope != "production") {
			t.Fatalf("runtime handoff lost its scoped stable identity: %+v", progress.Request)
		}
		verified := claimedIntentPlan(t, store, lease, desired)
		if err := runtime.EnsureEnvironmentGitOpsRuntime(t.Context(), lease, verified); err != nil {
			t.Fatal(err)
		}
		replayed, err := runtime.ReconcileEnvironmentGitOpsRuntime(t.Context(), lease, effect.ID)
		if err != nil || replayed.Ready || replayed.Request != nil {
			t.Fatalf("rapid replay should preserve pending work without requesting twice: %+v %v", replayed, err)
		}
		pending, err = runtime.PendingEnvironmentGitOpsRuntime(t.Context(), lease)
		if err != nil || len(pending) != 1 || pending[0].WakeID != effect.WakeID || !pending[0].RequiredAt.Equal(effect.RequiredAt) {
			t.Fatalf("polling changed freshness or duplicated effects: %+v %v", pending, err)
		}
		if err := store.UpdateInstanceState(t.Context(), old.ID, string(state.StateStopped)); err != nil {
			t.Fatal(err)
		}
		fresh := runtimeInstance(t, store, app, deployment, state.StateWaking)
		progress, err = runtime.ReconcileEnvironmentGitOpsRuntime(t.Context(), lease, effect.ID)
		if err != nil || progress.Ready || progress.Request != nil || !errors.Is(runtimeFinish(t, store, lease, desired), state.ErrConflict) {
			t.Fatalf("a fresh boot was accepted before readiness: %+v %v", progress, err)
		}
		if err := store.UpdateInstanceState(t.Context(), fresh.ID, string(state.StateRunning)); err != nil {
			t.Fatal(err)
		}
		progress, err = runtime.ReconcileEnvironmentGitOpsRuntime(t.Context(), lease, effect.ID)
		if err != nil || !progress.Ready || progress.Request != nil {
			t.Fatalf("ready fresh process failed runtime verification: %+v %v", progress, err)
		}
		if err := runtimeFinish(t, store, lease, desired); err != nil {
			t.Fatalf("verified runtime could not converge: %v", err)
		}
	})
}

func TestEnvironmentGitOpsRuntimeColdConvergence(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, lease, desired, app, deployment, _ := runtimeFixture(t, basic, false)
		// An unrelated resident environment must not force production to wake.
		neighbor, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:" + strings.Repeat("d", 64), Status: state.DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		runtimeInstance(t, store, app, neighbor, state.StateRunning)
		finishColdGitOpsRuntime(t, basic, lease)
		if _, err := store.LatestSnapshot(t.Context(), deployment.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("stale snapshot remained restorable: %v", err)
		}
		if err := runtimeFinish(t, store, lease, desired); err != nil {
			t.Fatal(err)
		}
	})
}

func TestEnvironmentGitOpsRuntimeDurableOutboxAndAdvancedBoundary(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	_, lease, _, app, _, _ := runtimeFixture(t, store, true)
	pending, err := store.PendingEnvironmentGitOpsRuntime(t.Context(), lease)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending: %+v %v", pending, err)
	}
	effect := pending[0]
	if _, err := store.ReconcileEnvironmentGitOpsRuntime(t.Context(), lease, effect.ID); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := pool.QueryRow(t.Context(), `SELECT payload FROM notification_outbox WHERE channel = $1`, db.NotifyRuntimeConfigRestart).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var request state.EnvironmentGitOpsRuntimeRequest
	if json.Unmarshal([]byte(raw), &request) != nil || request.AppID != app.ID || request.Scope != "production" || request.WakeID != effect.WakeID || strings.Contains(raw, "MODE") || strings.Contains(raw, "console") {
		t.Fatalf("outbox exposed values or lost request identity: %s", raw)
	}
	// A later committed change requires a new wake and a boundary visible to
	// schedd in the same transaction; wall-clock polling must never advance it.
	if err := store.SetEnvironmentGitOpsOverride(t.Context(), lease.Source.AccountID, lease.Source.ID, state.EnvironmentGitOpsOverrideRequest{
		Resource: "workload/api", Path: "variables/MODE", Reason: "reviewed maintenance", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppEnvInScope(t.Context(), lease.Source.AccountID, app.ID, "production", "MODE", "production"); err != nil {
		t.Fatal(err)
	}
	before, _, err := store.AppRuntimeConfigChangedAt(t.Context(), app.ID)
	if err != nil || !before.Equal(effect.RequiredAt) {
		t.Fatal("fixture advanced the scheduler stamp before runtime reconciliation", err)
	}
	if _, err := store.ReconcileEnvironmentGitOpsRuntime(t.Context(), lease, effect.ID); err != nil {
		t.Fatal(err)
	}
	stamp, exists, err := store.AppRuntimeConfigChangedAt(t.Context(), app.ID)
	if err != nil || !exists || !stamp.After(effect.RequiredAt) {
		t.Fatal("runtime reconciliation did not publish the committed variable boundary", err)
	}
	pending, err = store.PendingEnvironmentGitOpsRuntime(t.Context(), lease)
	if err != nil || len(pending) != 1 || pending[0].WakeID == effect.WakeID || !pending[0].RequiredAt.Equal(stamp) {
		t.Fatalf("new intent reused an already completed wake identity: %+v %v", pending, err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM notification_outbox WHERE channel = $1`, db.NotifyRuntimeConfigRestart).Scan(&count); err != nil || count != 2 {
		t.Fatalf("advanced boundary was not durably handed off: %d %v", count, err)
	}
}

func TestEnvironmentGitOpsRuntimeRecoveryIsFencedAndReportModeDoesNotRequest(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, old, _, _, _, _ := runtimeFixture(t, basic, true)
		runtime := basic.(state.EnvironmentGitOpsRuntimeStore)
		pending, err := runtime.PendingEnvironmentGitOpsRuntime(t.Context(), old)
		if err != nil || len(pending) != 1 {
			t.Fatalf("pending: %+v %v", pending, err)
		}
		effect := pending[0]
		mode := "report"
		if _, err := store.UpdateEnvironmentGitSource(t.Context(), old.Source.AccountID, old.Source.ID,
			state.EnvironmentGitSourceUpdate{ExpectedGeneration: old.Source.Generation, Mode: mode}); err != nil {
			t.Fatal(err)
		}
		fresh, err := store.ClaimEnvironmentGitOps(t.Context(), "replacement-runtime-worker", time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.ReconcileEnvironmentGitOpsRuntime(t.Context(), old, effect.ID); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("superseded worker retained runtime authority: %v", err)
		}
		pending, err = runtime.PendingEnvironmentGitOpsRuntime(t.Context(), fresh)
		if err != nil || len(pending) != 1 || pending[0].ID != effect.ID || pending[0].Generation != old.Source.Generation {
			t.Fatalf("source controls discarded committed runtime work: %+v %v", pending, err)
		}
		progress, err := runtime.ReconcileEnvironmentGitOpsRuntime(t.Context(), fresh, effect.ID)
		if err != nil || progress.Ready || progress.Request != nil {
			t.Fatalf("report mode requested replacement or hid stale residents: %+v %v", progress, err)
		}
		pending, err = runtime.PendingEnvironmentGitOpsRuntime(t.Context(), fresh)
		if err != nil || len(pending) != 1 || pending[0].RequestedAt != nil {
			t.Fatalf("report observation marked a refresh as requested: %+v %v", pending, err)
		}
	})
}

func TestEnvironmentGitOpsRuntimeEqualIntentStillRepairsStaleResidents(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, lease, desired, app, deployment, _ := runtimeFixture(t, basic, false)
		runtime := basic.(state.EnvironmentGitOpsRuntimeStore)
		finishColdGitOpsRuntime(t, basic, lease)
		runtimeInstance(t, store, app, deployment, state.StateRunning)
		if err := store.MarkAppRuntimeConfigChanged(t.Context(), app.ID); err != nil {
			t.Fatal(err)
		}
		plan := claimedIntentPlan(t, store, lease, desired)
		if plan.HasDrift() || !plan.CanApply() {
			t.Fatalf("fixture does not isolate runtime drift: %+v", plan)
		}
		if err := runtime.EnsureEnvironmentGitOpsRuntime(t.Context(), lease, plan); err != nil {
			t.Fatal(err)
		}
		pending, err := runtime.PendingEnvironmentGitOpsRuntime(t.Context(), lease)
		if err != nil || len(pending) != 1 || !errors.Is(runtimeFinish(t, store, lease, desired), state.ErrConflict) {
			t.Fatalf("equal intent skipped effective runtime drift: %+v %v", pending, err)
		}
	})
}
