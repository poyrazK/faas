package pgintegration_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
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
	if next == state.StateRunning {
		ackRuntimeInputs(t, store, app, deployment, instance)
	}
	return instance
}

// Simulate the scheduler's acknowledgement of the payload actually sent to
// vmmd. Fixtures must not substitute the instance's readiness timestamp.
func ackRuntimeInputs(t *testing.T, store state.Store, app state.App, deployment state.Deployment, instance state.Instance) {
	t.Helper()
	scope := deployment.Scope
	if scope == "" {
		scope = "default"
	}
	boundary, changed, err := state.RuntimeConfigChangedAtForScope(t.Context(), store, app.ID, scope)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		boundary = time.Unix(0, 0).UTC()
	}
	inputs := state.RuntimeConfigInputs{Scope: scope, Boundary: boundary,
		Variables: map[string]string{}, SecretVersions: map[string]int64{}, AllSecrets: true}
	variables, err := store.ListAppEnvInScope(t.Context(), app.AccountID, app.ID, scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range variables {
		inputs.Variables[row.Key] = row.Value
		if row.UpdatedAt.After(inputs.Boundary) {
			inputs.Boundary = row.UpdatedAt
		}
	}
	secrets, err := store.ListAppSecretsInScope(t.Context(), app.AccountID, app.ID, scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range secrets {
		inputs.SecretVersions[scope+"/"+row.Key] = row.DeliveryVersion
	}
	if err := store.(state.RuntimeConfigReceiptStore).RecordInstanceRuntimeConfigReceipt(t.Context(), instance.ID, instance.WakeID, inputs); err != nil {
		t.Fatal(err)
	}
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
		if err != nil || progress.Ready || !errors.Is(runtimeFinish(t, store, lease, desired), state.ErrConflict) {
			t.Fatalf("readiness without an input receipt was accepted: %+v %v", progress, err)
		}
		ackRuntimeInputs(t, store, app, deployment, fresh)
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
	before, _, err := store.AppRuntimeConfigChangedAtInScope(t.Context(), app.ID, "production")
	if err != nil || !before.Equal(effect.RequiredAt) {
		t.Fatal("fixture advanced the scheduler stamp before runtime reconciliation", err)
	}
	if _, err := store.ReconcileEnvironmentGitOpsRuntime(t.Context(), lease, effect.ID); err != nil {
		t.Fatal(err)
	}
	stamp, exists, err := store.AppRuntimeConfigChangedAtInScope(t.Context(), app.ID, "production")
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

func TestEnvironmentGitOpsScopedFreshnessPreservesNeighborManagedEnvironment(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store := basic.(intentTestStore)
		production, desired, app := intentFixture(t, store, "enforce")
		if _, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{
			AccountID: production.AccountID, ProjectID: production.ProjectID, Slug: "staging"}); err != nil {
			t.Fatal(err)
		}
		staging, err := store.CreateEnvironmentGitSource(t.Context(), production.AccountID, production.ProjectID, "staging",
			state.EnvironmentGitSourceSpec{RepositoryID: 123, InstallationID: 42, Repository: "example/shop", Ref: "refs/heads/main",
				ManifestPath: "environments/staging.yaml", Mode: "enforce", ApprovalPolicy: "manual"})
		if err != nil {
			t.Fatal(err)
		}
		stageDesired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion,
			Project: "shop", Environment: "staging", Workloads: map[string]api.EnvironmentWorkload{
				"api": {App: app.Slug, Variables: map[string]string{"MODE": "staging"}},
			}})
		if err != nil {
			t.Fatal(err)
		}
		staging, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(staging, stageDesired, strings.Repeat("b", 40)))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []state.EnvironmentGitSource{production, staging} {
			preview, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
			if err != nil || !preview.CanApply() {
				t.Fatalf("adoption: %+v %v", preview, err)
			}
			if err := store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, preview.Hash); err != nil {
				t.Fatal(err)
			}
		}
		deployments := map[string]state.Deployment{}
		instances := map[string]state.Instance{}
		for _, scope := range []string{"production", "staging"} {
			deployment, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: scope,
				Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("c", 64), Status: state.DeployLive})
			if err != nil {
				t.Fatal(err)
			}
			deployments[scope] = deployment
			instances[scope] = runtimeInstance(t, store, app, deployment, state.StateRunning)
			instance := instances[scope]
			if _, err := store.PublishSnapshotIfRuntimeFresh(t.Context(), state.Snapshot{DeploymentID: deployment.ID, FCVersion: "1.13.0", StorageKey: state.SnapMemKey(deployment.ID)}, instance.ID, instance.StartedAt); err != nil {
				t.Fatal(err)
			}
		}
		leases := map[string]state.EnvironmentGitOpsLease{}
		for range 2 {
			lease, err := store.ClaimEnvironmentGitOps(t.Context(), uuid.NewString(), time.Now(), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			leases[lease.Source.EnvironmentSlug] = lease
		}
		prodLease := leases["production"]
		if _, err := store.ApplyEnvironmentGitOps(t.Context(), prodLease, claimedIntentPlan(t, store, prodLease, desired)); err != nil {
			t.Fatal(err)
		}
		if _, shared, err := store.AppRuntimeConfigChangedAt(t.Context(), app.ID); err != nil || shared {
			t.Fatalf("scoped variables advanced a shared-credential boundary: %v %v", shared, err)
		}
		runtime := basic.(state.EnvironmentGitOpsRuntimeStore)
		stageTargets, err := runtime.ObserveEnvironmentGitOpsRuntime(t.Context(), leases["staging"])
		if err != nil || len(stageTargets) != 1 || !stageTargets[0].Ready() {
			t.Fatalf("production changed neighboring GitOps runtime: %+v %v", stageTargets, err)
		}
		if _, err := store.LatestSnapshot(t.Context(), deployments["staging"].ID); err != nil {
			t.Fatalf("production invalidated staging cache: %v", err)
		}
		publish := func(scope string) error {
			instance, deployment := instances[scope], deployments[scope]
			_, err := store.PublishSnapshotIfRuntimeFresh(t.Context(), state.Snapshot{DeploymentID: deployment.ID,
				FCVersion: "1.13.0", StorageKey: state.SnapshotCaptureMemKey(deployment.ID, state.SnapshotTierWarm, uuid.NewString()), Tier: state.SnapshotTierWarm}, instance.ID, instance.StartedAt)
			return err
		}
		if err := publish("staging"); err != nil {
			t.Fatalf("unchanged neighboring guest could not publish a fresh cache: %v", err)
		}
		if err := publish("production"); !errors.Is(err, state.ErrSnapshotRuntimeStale) {
			t.Fatalf("late capture from old production guest was accepted: %v", err)
		}
		// A shared credential still invalidates both environments and rejects
		// late captures, even though their scoped variable values are equal.
		if _, err := state.InvalidateAppSnapshots(t.Context(), store, app.ID); err != nil {
			t.Fatal(err)
		}
		stageTargets, err = runtime.ObserveEnvironmentGitOpsRuntime(t.Context(), leases["staging"])
		if err != nil || len(stageTargets) != 1 || stageTargets[0].Ready() || !errors.Is(publish("staging"), state.ErrSnapshotRuntimeStale) {
			t.Fatalf("shared credentials failed to invalidate affected environments: %+v %v", stageTargets, err)
		}
	})
}

func TestEnvironmentGitOpsScopedStampFencesConcurrentSnapshotPublication(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	_, _, _, app, deployment, _ := runtimeFixture(t, store, false)
	instance := runtimeInstance(t, store, app, deployment, state.StateRunning)
	writer, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback(t.Context()) }()
	if _, err := writer.Exec(t.Context(), `INSERT INTO app_runtime_config_scope_changes(app_id, scope, changed_at)
        VALUES ($1, 'production', clock_timestamp()) ON CONFLICT (app_id, scope)
        DO UPDATE SET changed_at = excluded.changed_at`, app.ID); err != nil {
		t.Fatal(err)
	}
	config := pool.Config().Copy()
	name := "gitops-snapshot-" + uuid.NewString()
	config.ConnConfig.RuntimeParams["application_name"] = name
	reader, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := state.NewPgStore(reader).PublishSnapshotIfRuntimeFresh(ctx, state.Snapshot{
			DeploymentID: deployment.ID, FCVersion: "1.13.0", Tier: state.SnapshotTierWarm,
			StorageKey: state.SnapshotCaptureMemKey(deployment.ID, state.SnapshotTierWarm, uuid.NewString()),
		}, instance.ID, instance.StartedAt)
		result <- err
	}()
	// Observe the database lock rather than treating a sleep or unreturned
	// goroutine as proof that publication serialized with the pending stamp.
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
            WHERE application_name = $1 AND wait_event_type = 'Lock')`, name).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("publication bypassed the pending scope stamp: %v", err)
		case <-ctx.Done():
			t.Fatal("publication never reached the stamp lock", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, state.ErrSnapshotRuntimeStale) {
			t.Fatalf("publication accepted the old guest after the scoped stamp committed: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("publication did not recover after stamp commit", ctx.Err())
	}
}

// adr: 567 — late readiness cannot refresh an already-admitted boot's stale inputs.
func TestEnvironmentGitOpsRuntimeReceiptFencesUncommittedInputWindow(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	_, lease, desired, app, deployment, _ := runtimeFixture(t, store, false)
	finishColdGitOpsRuntime(t, store, lease)
	// Admit before holding the input writer's revision lock. An already-admitted
	// boot can still read the earlier committed variables and become ready after
	// commit; the legacy started_at predicate would accept that process.
	instance := runtimeInstance(t, store, app, deployment, state.StateWaking)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	writer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback(t.Context()) }()
	var changedAt time.Time
	if err := writer.QueryRow(ctx, `UPDATE app_envs SET value = 'after-commit', updated_at = clock_timestamp()
        WHERE app_id = $1 AND scope = 'production' AND key = 'MANUAL' RETURNING updated_at`, app.ID).Scan(&changedAt); err != nil {
		t.Fatal(err)
	}
	if !instance.StartedAt.Before(changedAt) {
		t.Fatalf("fixture did not admit before the input write: %v >= %v", instance.StartedAt, changedAt)
	}
	boundary, stamped, err := state.RuntimeConfigChangedAtForScope(ctx, store, app.ID, "production")
	if err != nil || !stamped {
		t.Fatalf("prior boundary: %v %v", stamped, err)
	}
	inputs := state.RuntimeConfigInputs{Scope: "production", Boundary: boundary, Variables: map[string]string{}, AllSecrets: true}
	rows, err := store.ListAppEnvInScope(ctx, app.AccountID, app.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		inputs.Variables[row.Key] = row.Value
		if row.UpdatedAt.After(inputs.Boundary) {
			inputs.Boundary = row.UpdatedAt
		}
	}
	if inputs.Variables["MANUAL"] != "keep" {
		t.Fatalf("fixture did not read old committed inputs: %+v", inputs)
	}
	if _, err := writer.Exec(ctx, `INSERT INTO app_runtime_config_scope_changes(app_id, scope, changed_at)
        VALUES ($1, 'production', $2) ON CONFLICT (app_id, scope) DO UPDATE SET changed_at = excluded.changed_at`, app.ID, changedAt); err != nil {
		t.Fatal(err)
	}
	if err := writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateInstanceState(ctx, instance.ID, string(state.StateRunning)); err != nil {
		t.Fatal(err)
	}
	if err := store.SetInstanceRuntime(ctx, instance.ID, "receipt-window", "10.100.0.2", 20001); err != nil {
		t.Fatal(err)
	}
	instance, err = store.InstanceByID(ctx, instance.ID)
	if err != nil || !instance.StartedAt.After(changedAt) {
		t.Fatalf("late readiness: %+v %v", instance, err)
	}
	if err := store.RecordInstanceRuntimeConfigReceipt(ctx, instance.ID, instance.WakeID, inputs); err != nil {
		t.Fatal(err)
	}
	targets, err := store.ObserveEnvironmentGitOpsRuntime(ctx, lease)
	if err != nil || len(targets) != 1 || targets[0].StaleResidents != 1 || targets[0].Ready() {
		t.Fatalf("uncommitted-window boot passed runtime proof: %+v %v", targets, err)
	}
	if _, err := store.PublishSnapshotIfRuntimeFresh(ctx, state.Snapshot{DeploymentID: deployment.ID,
		FCVersion: "1.13.0", Tier: state.SnapshotTierWarm,
		StorageKey: state.SnapshotCaptureMemKey(deployment.ID, state.SnapshotTierWarm, uuid.NewString()),
	}, instance.ID, instance.StartedAt); !errors.Is(err, state.ErrSnapshotRuntimeStale) {
		t.Fatalf("uncommitted-window guest published a reusable cache: %v", err)
	}
	if !errors.Is(runtimeFinish(t, store, lease, desired), state.ErrConflict) {
		t.Fatal("late readiness published convergence without current boot inputs")
	}
}
