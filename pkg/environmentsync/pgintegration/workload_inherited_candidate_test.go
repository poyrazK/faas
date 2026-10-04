package pgintegration_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func inheritedImageCandidateFixture(t *testing.T, basic gitOpsTestStore) (intentTestStore, state.EnvironmentGitOpsLease, environmentsync.DesiredState, state.App, state.Deployment, environmentsync.Plan) {
	t.Helper()
	store, source, desired, app, previous, _ := workloadIntentFixture(t, basic, "enforce")
	full := basic.(state.Store)
	if err := full.UpsertAppSecretInScope(t.Context(), source.AccountID, app.ID, "production", "TOKEN", []byte("sealed-original")); err != nil {
		t.Fatal(err)
	}
	forceSharedBase := false
	current, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: previous.ImageDigest,
		OverrideEntrypoint: []string{"./original"}, OverrideCmd: []string{"--original"}, OverrideEnv: json.RawMessage(`{"STATIC":"retained"}`),
		OverrideEnvSecrets: json.RawMessage(`{"TOKEN":"secret:TOKEN"}`), OverridePort: 8079,
		OverrideHealthcheck: json.RawMessage(`{"path":"/health"}`), OverrideLivenessProbe: json.RawMessage(`{"path":"/alive"}`),
		OverrideReadinessProbe: json.RawMessage(`{"path":"/ready"}`), OverrideMainDependsOn: json.RawMessage(`[{"workload":"metrics","condition":"started"}]`),
		Sidecars:            json.RawMessage(`[{"name":"metrics","type":"sidecar","image":"registry.example/metrics@sha256:` + strings.Repeat("e", 64) + `","env":{"TOKEN":"c2VhbGVk"},"secret_reload_signal":"SIGUSR1"}]`),
		FullRootfsAllowAuto: true, FullRootfsOverride: &forceSharedBase, ReleaseCommand: []string{"./release"}, DisableStartupCPUBoost: true, RollbackOn5xx: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(t.Context(), previous.ID, state.DeploySuperseded, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(t.Context(), current.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	workload := desired.Definition.Workloads["api"]
	workload.Source = nil
	desired.Definition.Workloads["api"] = workload
	desired, err = environmentsync.Compile(desired.Definition)
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("b", 40)))
	if err != nil {
		t.Fatal(err)
	}
	adoptWorkloadIntent(t, store, source)
	lease, err := store.ClaimEnvironmentGitOps(t.Context(), "inherited-image-preparer", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyEnvironmentGitOps(t.Context(), lease, claimedIntentPlan(t, store, lease, desired)); err != nil {
		t.Fatal(err)
	}
	return store, lease, desired, app, current, claimedIntentPlan(t, store, lease, desired)
}

func TestEnvironmentGitOpsRuntimeOnlyCandidatePreservesUnmanagedDeployment(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, lease, desired, app, current, plan := inheritedImageCandidateFixture(t, basic)
		preparer := basic.(state.EnvironmentGitOpsPreparationStore)
		rows, err := preparer.PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan)
		if err != nil || len(rows) != 1 {
			t.Fatalf("prepare inherited image: %+v %v", rows, err)
		}
		dep, err := store.DeploymentByID(t.Context(), rows[0].DeploymentID)
		if err != nil || dep.ImageDigest != current.ImageDigest || !dep.EnvironmentWorkloadHeld() || dep.Status != state.DeployPending {
			t.Fatalf("inherited candidate: %+v %v", dep, err)
		}
		frozen, err := dep.ScopedWorkloadRuntime()
		if err != nil || frozen.DeploymentInputs == nil || !reflect.DeepEqual(frozen.SourceDeployments, []string{current.ID}) || string(frozen.Runtime["port"]) != "8080" {
			t.Fatalf("missing inherited identity/inputs: %+v %v", frozen, err)
		}
		if !reflect.DeepEqual(dep.OverrideEntrypoint, current.OverrideEntrypoint) || !reflect.DeepEqual(dep.OverrideCmd, current.OverrideCmd) ||
			dep.OverridePort != current.OverridePort || !dep.FullRootfsAllowAuto || dep.FullRootfsOverride == nil || *dep.FullRootfsOverride ||
			!dep.DisableStartupCPUBoost || !dep.RollbackOn5xx || !reflect.DeepEqual(dep.ReleaseCommand, current.ReleaseCommand) {
			t.Fatalf("deployment settings were dropped: %+v", dep)
		}
		for key, raw := range map[string]json.RawMessage{"env": dep.OverrideEnv, "secret_refs": dep.OverrideEnvSecrets, "health": dep.OverrideHealthcheck, "liveness": dep.OverrideLivenessProbe, "readiness": dep.OverrideReadinessProbe, "dependencies": dep.OverrideMainDependsOn, "sidecars": dep.Sidecars} {
			if len(raw) == 0 || string(raw) == "{}" || string(raw) == "[]" {
				t.Fatalf("%s inherited input disappeared", key)
			}
		}
		if strings.Contains(string(dep.Sidecars), "secret_reload_signal") || !strings.Contains(string(dep.Sidecars), "c2VhbGVk") {
			t.Fatalf("derived metadata or sealed sidecar input projection: %s", dep.Sidecars)
		}
		row := scopedWorkloadIntent(t, basic, lease.Source, app.ID, lease.Source.EnvironmentID)
		if row.Source != nil {
			t.Fatal("runtime-only preparation imported unmanaged source intent")
		}
		observed, err := store.ObserveEnvironmentGitOps(t.Context(), lease, desired)
		if err != nil {
			t.Fatal(err)
		}
		for _, owner := range observed.Owners {
			if owner.Resource == "workload/api" && owner.Path == "source" {
				t.Fatal("runtime-only preparation transferred source ownership")
			}
		}
		if again, err := preparer.PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan); err != nil || len(again) != 1 || again[0].DeploymentID != dep.ID {
			t.Fatalf("retry changed inherited candidate: %+v %v", again, err)
		}
		if _, err := basic.(state.Store).UpdateDeploymentMinInstances(t.Context(), dep.ID, 1); err == nil {
			t.Fatal("ordinary edit changed frozen candidate inputs")
		}
		if _, err := basic.(state.Store).UpdateDeploymentMinInstances(t.Context(), current.ID, 1); err != nil {
			t.Fatal(err)
		}
		if _, err := preparer.PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("changed inherited input accepted stale review: %v", err)
		}
		dep, _ = store.DeploymentByID(t.Context(), dep.ID)
		if dep.MinInstances != 0 {
			t.Fatal("prepared candidate inherited a later edit")
		}
	})
}

func TestPgEnvironmentGitOpsInheritedCandidateRawInputFences(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	_, lease, _, _, _, plan := inheritedImageCandidateFixture(t, store)
	rows, err := store.PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan)
	if err != nil || len(rows) != 1 {
		t.Fatalf("prepare: %+v %v", rows, err)
	}
	id := rows[0].DeploymentID
	for _, query := range []string{
		`update deployments set min_instances=1 where id=$1`,
		`update deployments set full_rootfs_override=true where id=$1`,
		`update deployments set disable_startup_cpu_boost=false where id=$1`,
		`update deployments set override_env='{"STATIC":"changed"}' where id=$1`,
		`update deployments set sidecars='[]' where id=$1`,
		`update deployments set workflows='[{"name":"changed"}]' where id=$1`,
	} {
		if _, err := pool.Exec(t.Context(), query, id); err == nil {
			t.Fatalf("raw SQL changed inherited inputs: %s", query)
		}
	}
	// Derived OCI metadata is populated by imaging, independently of intent.
	if err := store.SetDeploymentSecretReloadSignal(t.Context(), id, "SIGUSR2"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentSidecarSecretReloadSignal(t.Context(), id, "metrics", "SIGUSR1"); err != nil {
		t.Fatal(err)
	}
	dep, _ := store.DeploymentByID(t.Context(), id)
	if _, err := dep.ScopedWorkloadRuntime(); err != nil {
		t.Fatalf("derived metadata invalidated candidate input: %v", err)
	}
}

func TestEnvironmentGitOpsInheritedCandidateRequiresConsistentImmutableSource(t *testing.T) {
	for _, reason := range []string{"missing", "mutable", "conflicting_inputs"} {
		t.Run(reason, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				store, lease, desired, app, current, _ := inheritedImageCandidateFixture(t, basic)
				if reason != "missing" {
					image := current.ImageDigest
					if reason == "mutable" {
						image = "registry.example/shop:latest"
					}
					other, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage,
						ImageDigest: image, CanaryPreset: "slow", CanaryTotalSteps: 2, TrafficPercentExplicit: true,
						OverrideEntrypoint: []string{"./other-live-command"}})
					if err != nil {
						t.Fatal(err)
					}
					if err := store.UpdateDeploymentStatus(t.Context(), other.ID, state.DeployLive, ""); err != nil {
						t.Fatal(err)
					}
				}
				if reason != "conflicting_inputs" {
					if err := store.UpdateDeploymentStatus(t.Context(), current.ID, state.DeploySuperseded, ""); err != nil {
						t.Fatal(err)
					}
				}
				plan := claimedIntentPlan(t, store, lease, desired)
				if _, err := basic.(state.EnvironmentGitOpsPreparationStore).PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan); !errors.Is(err, state.ErrEnvironmentWorkloadPreparationUnavailable) {
					t.Fatalf("%s inherited source was prepared: %v", reason, err)
				}
				rows, err := basic.ListDeploymentsForOperator(t.Context(), state.OperatorDeploymentFilter{AppID: app.ID, Limit: 20})
				if err != nil {
					t.Fatal(err)
				}
				for _, row := range rows {
					if row.EnvironmentWorkloadHeld() {
						t.Fatal("unavailable adapter left a partially prepared candidate")
					}
				}
			})
		})
	}
}
