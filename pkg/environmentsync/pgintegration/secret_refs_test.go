package pgintegration_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

type secretRefTestStore interface {
	intentTestStore
	state.AppEnvironmentSecretReferenceStore
	state.RuntimeConfigReceiptStore
}

func secretRefFixture(t *testing.T, basic gitOpsTestStore, mode string) (secretRefTestStore, state.EnvironmentGitSource, environmentsync.DesiredState, state.App) {
	t.Helper()
	store := basic.(secretRefTestStore)
	source, base := seedMode(t, basic, mode)
	app, err := store.CreateApp(t.Context(), state.App{AccountID: source.AccountID, ProjectID: source.ProjectID, Slug: "shop-api", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: source.AccountID, ProjectID: source.ProjectID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"production", "staging"} {
		for _, name := range []string{"DATABASE_A", "DATABASE_B"} {
			if err := store.UpsertAppSecretInScope(t.Context(), source.AccountID, app.ID, scope, name, []byte(scope+"-sealed-"+name)); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.PutAppEnvironmentSecretReference(t.Context(), source.AccountID, app.ID, scope, "DATABASE_URL", "secret:DATABASE_A"); err != nil {
			t.Fatal(err)
		}
	}
	definition := base.Definition
	definition.Workloads["api"] = api.EnvironmentWorkload{App: app.Slug, SecretRefs: map[string]string{"DATABASE_URL": "secret:DATABASE_B"}}
	desired, err := environmentsync.Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("a", 40)))
	if err != nil {
		t.Fatal(err)
	}
	return store, source, desired, app
}

func assertSecretRef(t *testing.T, store secretRefTestStore, source state.EnvironmentGitSource, app state.App, scope, key, ref string) {
	t.Helper()
	refs, err := store.AppEnvironmentSecretReferences(t.Context(), source.AccountID, app.ID, scope)
	if err != nil || refs[key] != ref {
		t.Fatalf("reference %s/%s: %+v %v; want %s", scope, key, refs, err, ref)
	}
}

func adoptSecretRefs(t *testing.T, store secretRefTestStore, source state.EnvironmentGitSource) {
	t.Helper()
	preview, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
	if err != nil || !preview.CanApply() {
		t.Fatalf("adoption: %+v %v", preview, err)
	}
	if err := store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, preview.Hash); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentGitOpsSecretReferencesAdoptionAndEnforcement(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, source, desired, app := secretRefFixture(t, basic, "enforce")
		preview, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
		if err != nil || !preview.CanApply() {
			t.Fatalf("preview: %+v %v", preview, err)
		}
		if err := store.PutAppEnvironmentSecretReference(t.Context(), source.AccountID, app.ID, "production", "DATABASE_URL", "secret:DATABASE_B"); err != nil {
			t.Fatal(err)
		}
		if err := store.AdoptEnvironmentGitOps(t.Context(), source.AccountID, source.ID, preview.Hash); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("stale review adopted references: %v", err)
		}
		if err := store.PutAppEnvironmentSecretReference(t.Context(), source.AccountID, app.ID, "production", "DATABASE_URL", "secret:DATABASE_A"); err != nil {
			t.Fatal(err)
		}
		adoptSecretRefs(t, store, source)
		assertSecretRef(t, store, source, app, "production", "DATABASE_URL", "secret:DATABASE_A")
		for _, mutate := range []func() error{
			func() error {
				return store.PutAppEnvironmentSecretReference(t.Context(), source.AccountID, app.ID, "production", "DATABASE_URL", "secret:DATABASE_B")
			},
			func() error {
				return store.DeleteAppEnvironmentSecretReference(t.Context(), source.AccountID, app.ID, "production", "DATABASE_URL")
			},
			func() error {
				return store.UpsertAppEnvInScope(t.Context(), source.AccountID, app.ID, "production", "DATABASE_URL", "shadow")
			},
		} {
			if err := mutate(); !errors.Is(err, state.ErrEnvironmentGitManaged) {
				t.Fatalf("owned write bypassed contract: %v", err)
			}
		}
		if _, err := store.AppEnvironmentSecretReferences(t.Context(), "00000000-0000-0000-0000-000000000000", app.ID, "production"); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("cross-account reference read: %v", err)
		}
		lease, err := store.ClaimEnvironmentGitOps(t.Context(), "reference-worker", time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		plan := claimedIntentPlan(t, store, lease, desired)
		if !plan.CanApply() || !plan.HasDrift() {
			t.Fatalf("reference plan: %+v", plan)
		}
		if _, err := store.(state.EnvironmentGitOpsEffectStore).ApplyEnvironmentGitOpsWithEffects(t.Context(), lease, plan, nil); err != nil {
			t.Fatal(err)
		}
		assertSecretRef(t, store, source, app, "production", "DATABASE_URL", "secret:DATABASE_B")
		assertSecretRef(t, store, source, app, "staging", "DATABASE_URL", "secret:DATABASE_A")
		secrets, err := store.ListAppSecretsInScope(t.Context(), source.AccountID, app.ID, "production")
		if err != nil || len(secrets) != 2 {
			t.Fatalf("reference change removed values: %+v %v", secrets, err)
		}
		for _, row := range secrets {
			if !bytes.Equal(row.Ciphertext, []byte("production-sealed-"+row.Key)) {
				t.Fatal("reference change modified a sealed value")
			}
		}
		pending, err := basic.(state.EnvironmentGitOpsRuntimeStore).PendingEnvironmentGitOpsRuntime(t.Context(), lease)
		if err != nil || len(pending) != 1 || pending[0].Environment != "production" {
			t.Fatalf("missing scoped runtime effect: %+v %v", pending, err)
		}
	})
}

func TestEnvironmentGitOpsSecretReferenceRuntimeProof(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, source, desired, app := secretRefFixture(t, basic, "enforce")
		adoptSecretRefs(t, store, source)
		lease, err := store.ClaimEnvironmentGitOps(t.Context(), "reference-runtime-worker", time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.(state.EnvironmentGitOpsEffectStore).ApplyEnvironmentGitOpsWithEffects(t.Context(), lease, claimedIntentPlan(t, store, lease, desired), nil); err != nil {
			t.Fatal(err)
		}
		secrets, err := store.ListAppSecretsInScope(t.Context(), source.AccountID, app.ID, "production")
		if err != nil {
			t.Fatal(err)
		}
		versions := map[string]int64{}
		for _, row := range secrets {
			versions["production/"+row.Key] = row.DeliveryVersion
		}
		inputs := state.RuntimeConfigInputs{Scope: "production", Boundary: time.Now().UTC(), Variables: map[string]string{}, SecretVersions: versions, SecretRefs: map[string]string{"DATABASE_URL": "secret:DATABASE_B"}}
		for _, test := range []struct {
			name  string
			refs  map[string]string
			fresh bool
		}{
			{"approved alias", maps.Clone(inputs.SecretRefs), true},
			{"wrong target with current versions", map[string]string{"DATABASE_URL": "secret:DATABASE_A"}, false},
			{"old receipt without mapping", map[string]string{}, false},
			{"missing destination", map[string]string{"OTHER": "secret:DATABASE_B"}, false},
		} {
			t.Run(test.name, func(t *testing.T) {
				candidate := inputs
				candidate.SecretRefs = test.refs
				fresh, err := store.RuntimeConfigInputsFresh(t.Context(), app.ID, candidate)
				if err != nil || fresh != test.fresh {
					t.Fatalf("fresh=%v %v; want %v", fresh, err, test.fresh)
				}
			})
		}
		deployment, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("b", 64), Status: state.DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		instance := runtimeInstance(t, store, app, deployment, state.StateWaking)
		if err := store.UpdateInstanceState(t.Context(), instance.ID, string(state.StateRunning)); err != nil {
			t.Fatal(err)
		}
		if err := store.RecordInstanceRuntimeConfigReceipt(t.Context(), instance.ID, instance.WakeID, inputs); err != nil {
			t.Fatal(err)
		}
		inputs.SecretRefs["DATABASE_URL"] = "secret:DATABASE_A"
		stored, exists, err := store.InstanceRuntimeConfigReceipt(t.Context(), instance.ID)
		if err != nil || !exists || stored.SecretRefs["DATABASE_URL"] != "secret:DATABASE_B" {
			t.Fatalf("receipt alias mutated through input: %+v %v %v", stored, exists, err)
		}
		if err := store.RecordInstanceRuntimeConfigReceipt(t.Context(), instance.ID, instance.WakeID, inputs); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("same wake rewrote mapping: %v", err)
		}
		snapshot, err := store.PublishSnapshotIfRuntimeFresh(t.Context(), state.Snapshot{DeploymentID: deployment.ID, FCVersion: "1.13.0", StorageKey: state.SnapMemKey(deployment.ID)}, instance.ID, instance.StartedAt)
		if err != nil {
			t.Fatal(err)
		}
		captured, exists, err := store.SnapshotRuntimeConfigReceipt(t.Context(), snapshot.ID)
		if err != nil || !exists || !maps.Equal(captured.SecretRefs, stored.SecretRefs) {
			t.Fatalf("snapshot lost reference evidence: %+v %v %v", captured, exists, err)
		}
		if err := store.UpsertAppSecretInScope(t.Context(), source.AccountID, app.ID, "production", "DATABASE_B", []byte("new-sealed-value")); err != nil {
			t.Fatal(err)
		}
		stored.Boundary = time.Now().UTC()
		if fresh, err := store.RuntimeConfigInputsFresh(t.Context(), app.ID, stored); err != nil || fresh {
			t.Fatalf("old secret version qualified new mapping: %v %v", fresh, err)
		}
		if _, err := store.PublishSnapshotIfRuntimeFresh(t.Context(), state.Snapshot{DeploymentID: deployment.ID, FCVersion: "1.13.0", StorageKey: state.SnapMemKey(deployment.ID)}, instance.ID, instance.StartedAt); !errors.Is(err, state.ErrSnapshotRuntimeStale) {
			t.Fatalf("rotation allowed recapture with stale input evidence: %v", err)
		}
		plan := claimedIntentPlan(t, store, lease, desired)
		if plan.HasDrift() || !plan.CanApply() {
			t.Fatalf("secret rotation changed Git intent: %+v", plan)
		}
	})
}

func TestEnvironmentGitOpsSecretReferenceMissingAndShadowedTargetsBlock(t *testing.T) {
	for _, invalid := range []string{"missing", "plaintext"} {
		t.Run(invalid, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				store, source, desired, app := secretRefFixture(t, basic, "enforce")
				if invalid == "missing" {
					definition := desired.Definition
					w := definition.Workloads["api"]
					w.SecretRefs["DATABASE_URL"] = "secret:MISSING"
					definition.Workloads["api"] = w
					var err error
					desired, err = environmentsync.Compile(definition)
					if err != nil {
						t.Fatal(err)
					}
					source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("b", 40)))
					if err != nil {
						t.Fatal(err)
					}
				} else if err := store.UpsertAppEnvInScope(t.Context(), source.AccountID, app.ID, "production", "DATABASE_URL", "shadow"); err != nil {
					t.Fatal(err)
				}
				preview, err := store.PreviewEnvironmentGitOpsAdoption(t.Context(), source.AccountID, source.ID)
				if err != nil || preview.CanApply() {
					t.Fatalf("unsafe adoption: %+v %v", preview, err)
				}
				lease, err := store.ClaimEnvironmentGitOps(t.Context(), "blocked-reference-worker", time.Now(), time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				plan := claimedIntentPlan(t, store, lease, desired)
				if plan.CanApply() {
					t.Fatalf("invalid references allowed: %+v", plan)
				}
				if _, err := store.(state.EnvironmentGitOpsEffectStore).ApplyEnvironmentGitOpsWithEffects(t.Context(), lease, plan, nil); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("blocked plan applied: %v", err)
				}
				assertSecretRef(t, store, source, app, "production", "DATABASE_URL", "secret:DATABASE_A")
			})
		})
	}
}

func TestEnvironmentGitOpsSecretReferenceReportDrift(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, source, _, app := secretRefFixture(t, basic, "report")
		adoptSecretRefs(t, store, source)
		before, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "production")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.DeleteAppEnvironmentSecretReference(t.Context(), source.AccountID, app.ID, "production", "DATABASE_URL"); err != nil {
			t.Fatal(err)
		}
		after, err := store.EnvironmentGitSource(t.Context(), source.AccountID, source.ProjectID, "production")
		if err != nil || after.IntentVersion <= before.IntentVersion {
			t.Fatalf("report mutation did not schedule observation: %+v %v", after, err)
		}
		worker := environmentgitops.Worker{Store: store, Backend: environmentgitops.IntentBackend{Store: store}, LeaseDuration: time.Minute, CheckInterval: time.Minute, RetryInterval: time.Second}
		if worked, err := worker.RunOnce(t.Context()); err != nil || !worked {
			t.Fatalf("report worker: %v %v", worked, err)
		}
		runs, err := store.ListEnvironmentGitOpsRuns(t.Context(), source.AccountID, source.ID, 10)
		if err != nil || len(runs) != 1 || runs[0].Status != "drifted" || !strings.Contains(string(runs[0].Plan), "secret_refs/DATABASE_URL") {
			t.Fatalf("reference drift missing: %+v %v", runs, err)
		}
		assertSecretRef(t, store, source, app, "production", "DATABASE_URL", "")
	})
}

func TestEnvironmentGitOpsSecretReferencePruneReusesQuotaAndKeepsValues(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, source, desired, app := secretRefFixture(t, basic, "enforce")
		adoptSecretRefs(t, store, source)
		// Two reference rows already count toward the app's cross-environment quota.
		for i := 2; i < api.MustLimitsFor(api.PlanPro).EnvVarsMax; i++ {
			if err := store.UpsertAppEnvInScope(t.Context(), source.AccountID, app.ID, "staging", fmt.Sprintf("UNMANAGED_%d", i), "keep"); err != nil {
				t.Fatal(err)
			}
		}
		prune := true
		var err error
		source, err = store.UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: source.Generation, Prune: &prune})
		if err != nil {
			t.Fatal(err)
		}
		definition := desired.Definition
		w := definition.Workloads["api"]
		w.SecretRefs = map[string]string{"NEW_DATABASE_URL": "secret:DATABASE_B"}
		definition.Workloads["api"] = w
		desired, err = environmentsync.Compile(definition)
		if err != nil {
			t.Fatal(err)
		}
		source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("b", 40)))
		if err != nil {
			t.Fatal(err)
		}
		lease, err := store.ClaimEnvironmentGitOps(t.Context(), "reference-prune-worker", time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		plan := claimedIntentPlan(t, store, lease, desired)
		if !plan.CanApply() {
			t.Fatalf("reviewed prune did not reclaim quota: %+v", plan)
		}
		if _, err := store.(state.EnvironmentGitOpsEffectStore).ApplyEnvironmentGitOpsWithEffects(t.Context(), lease, plan, nil); err != nil {
			t.Fatal(err)
		}
		assertSecretRef(t, store, source, app, "production", "DATABASE_URL", "")
		assertSecretRef(t, store, source, app, "production", "NEW_DATABASE_URL", "secret:DATABASE_B")
		intent, err := basic.(state.AppEnvironmentSecretIntentReader).AppEnvironmentSecretIntent(t.Context(), source.AccountID, app.ID, "production")
		if err != nil || intent.EffectiveReferences(map[string]string{"DATABASE_URL": "secret:DATABASE_A"})["DATABASE_URL"] != "" {
			t.Fatalf("pruned legacy mapping reappeared: %+v %v", intent, err)
		}

		rows, err := store.ListAppSecretsInScope(t.Context(), source.AccountID, app.ID, "production")
		if err != nil || len(rows) != 2 {
			t.Fatalf("prune removed sealed values: %+v %v", rows, err)
		}
	})
}

func TestEnvironmentSecretReferencesCatalogRecreationAndQuota(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, source, _, app := secretRefFixture(t, basic, "enforce")
		original, err := store.ProjectEnvironmentBySlug(t.Context(), source.AccountID, source.ProjectID, "staging")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.DeleteProjectEnvironment(t.Context(), source.AccountID, source.ProjectID, "staging"); err != nil {
			t.Fatal(err)
		}
		replacement, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: source.AccountID, ProjectID: source.ProjectID, Slug: "staging"})
		if err != nil || original.ID == replacement.ID {
			t.Fatalf("recreation identity: %+v %v", replacement, err)
		}
		assertSecretRef(t, store, source, app, "staging", "DATABASE_URL", "")
		for i := 1; i < api.MustLimitsFor(api.PlanPro).EnvVarsMax; i++ {
			if err := store.PutAppEnvironmentSecretReference(t.Context(), source.AccountID, app.ID, "staging", fmt.Sprintf("REF_%d", i), "secret:DATABASE_B"); err != nil {
				t.Fatalf("deleted identity consumed quota: %v", err)
			}
		}
		if count, err := store.CountAppEnv(t.Context(), source.AccountID, app.ID); err != nil || count != api.MustLimitsFor(api.PlanPro).EnvVarsMax {
			t.Fatalf("reference slots omitted from shared quota: %d %v", count, err)
		}
		if count, err := store.CountAppEnvInScope(t.Context(), source.AccountID, app.ID, "staging"); err != nil || count != api.MustLimitsFor(api.PlanPro).EnvVarsMax-1 {
			t.Fatalf("scoped reference count: %d %v", count, err)
		}
		if err := store.PutAppEnvironmentSecretReference(t.Context(), source.AccountID, app.ID, "staging", "EXCESS", "secret:DATABASE_B"); !errors.Is(err, state.ErrQuotaExceeded) {
			t.Fatalf("reference quota bypassed: %v", err)
		}
		if err := store.PutAppEnvironmentSecretReference(t.Context(), source.AccountID, app.ID, "staging", "REF_1", "plain"); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("malformed reference accepted: %v", err)
		}
		refs, err := store.AppEnvironmentSecretReferences(t.Context(), source.AccountID, app.ID, "production")
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(refs)
		if bytes.Contains(raw, []byte("sealed")) {
			t.Fatal("reference observation contains sealed value")
		}
	})
}

func TestEnvironmentGitOpsSecretReferencesFenceServingConvergence(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, source, desired, app := secretRefFixture(t, basic, "enforce")
		adoptSecretRefs(t, store, source)
		lease, err := store.ClaimEnvironmentGitOps(t.Context(), "serving-reference-worker", time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.(state.EnvironmentGitOpsEffectStore).ApplyEnvironmentGitOpsWithEffects(t.Context(), lease, claimedIntentPlan(t, store, lease, desired), nil); err != nil {
			t.Fatal(err)
		}
		deployment, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("b", 64), Status: state.DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		old := runtimeInstance(t, store, app, deployment, state.StateWaking)
		if err := store.UpdateInstanceState(t.Context(), old.ID, string(state.StateRunning)); err != nil {
			t.Fatal(err)
		}
		rows, err := store.ListAppSecretsInScope(t.Context(), source.AccountID, app.ID, "production")
		if err != nil {
			t.Fatal(err)
		}
		versions := map[string]int64{}
		for _, row := range rows {
			versions["production/"+row.Key] = row.DeliveryVersion
		}
		wrong := state.RuntimeConfigInputs{Scope: "production", Boundary: time.Now().UTC(), Variables: map[string]string{}, SecretVersions: versions, SecretRefs: map[string]string{"DATABASE_URL": "secret:DATABASE_A"}}
		if err := store.RecordInstanceRuntimeConfigReceipt(t.Context(), old.ID, old.WakeID, wrong); err != nil {
			t.Fatal(err)
		}
		runtime := basic.(state.EnvironmentGitOpsRuntimeStore)
		pending, err := runtime.PendingEnvironmentGitOpsRuntime(t.Context(), lease)
		if err != nil || len(pending) != 1 {
			t.Fatalf("runtime work: %+v %v", pending, err)
		}
		if err := runtimeFinish(t, store, lease, desired); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("equal Git intent hid wrong serving alias: %v", err)
		}
		progress, err := runtime.ReconcileEnvironmentGitOpsRuntime(t.Context(), lease, pending[0].ID)
		if err != nil || progress.Ready {
			t.Fatalf("wrong mapping qualified serving fleet: %+v %v", progress, err)
		}
		if err := store.UpdateInstanceState(t.Context(), old.ID, string(state.StateStopped)); err != nil {
			t.Fatal(err)
		}
		fresh := runtimeInstance(t, store, app, deployment, state.StateWaking)
		correct := wrong
		correct.Boundary = time.Now().UTC()
		correct.SecretRefs = map[string]string{"DATABASE_URL": "secret:DATABASE_B"}
		if err := store.UpdateInstanceState(t.Context(), fresh.ID, string(state.StateRunning)); err != nil {
			t.Fatal(err)
		}
		if err := store.RecordInstanceRuntimeConfigReceipt(t.Context(), fresh.ID, fresh.WakeID, correct); err != nil {
			t.Fatal(err)
		}
		progress, err = runtime.ReconcileEnvironmentGitOpsRuntime(t.Context(), lease, pending[0].ID)
		if err != nil || !progress.Ready {
			t.Fatalf("correct serving mapping did not qualify: %+v %v", progress, err)
		}
		if err := runtimeFinish(t, store, lease, desired); err != nil {
			t.Fatal(err)
		}
	})
}

func TestEnvironmentGitOpsSecretReferenceOverrideRetainsThenRestores(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, source, desired, app := secretRefFixture(t, basic, "enforce")
		adoptSecretRefs(t, store, source)
		request := state.EnvironmentGitOpsOverrideRequest{Resource: "workload/api", Path: "secret_refs/DATABASE_URL", Reason: "temporary database incident", ExpiresAt: time.Now().UTC().Add(time.Hour)}
		if err := store.SetEnvironmentGitOpsOverride(t.Context(), source.AccountID, source.ID, request); err != nil {
			t.Fatal(err)
		}
		if err := store.PutAppEnvironmentSecretReference(t.Context(), source.AccountID, app.ID, "production", "DATABASE_URL", "secret:DATABASE_A"); err != nil {
			t.Fatal(err)
		}
		lease, err := store.ClaimEnvironmentGitOps(t.Context(), "reference-override-worker", time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		plan := claimedIntentPlan(t, store, lease, desired)
		if !plan.CanApply() || !plan.HasDrift() {
			t.Fatalf("override hidden from drift: %+v", plan)
		}
		if _, err := store.(state.EnvironmentGitOpsEffectStore).ApplyEnvironmentGitOpsWithEffects(t.Context(), lease, plan, nil); err != nil {
			t.Fatal(err)
		}
		assertSecretRef(t, store, source, app, "production", "DATABASE_URL", "secret:DATABASE_A")
		if err := runtimeFinish(t, store, lease, desired); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("override declared converged: %v", err)
		}
		if err := store.RemoveEnvironmentGitOpsOverride(t.Context(), source.AccountID, source.ID, "workload/api", "secret_refs/DATABASE_URL"); err != nil {
			t.Fatal(err)
		}
		plan = claimedIntentPlan(t, store, lease, desired)
		if _, err := store.(state.EnvironmentGitOpsEffectStore).ApplyEnvironmentGitOpsWithEffects(t.Context(), lease, plan, nil); err != nil {
			t.Fatal(err)
		}
		assertSecretRef(t, store, source, app, "production", "DATABASE_URL", "secret:DATABASE_B")
	})
}
