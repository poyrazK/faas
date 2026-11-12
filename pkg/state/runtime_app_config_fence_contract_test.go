// adr: 590
package state_test

import (
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemRuntimeInstancePublicationConfiguration(t *testing.T) {
	testRuntimeInstancePublicationConfiguration(t, state.NewMemStore())
}

func testRuntimeInstancePublicationConfiguration(t *testing.T, store runtimeAppEnvTestStore) {
	node := runtimeSecretNodeForTest(t, store)
	for _, change := range []string{"insert", "edit", "delete", "recreate", "artifact", "sidecar-layer", "sidecar-signal", "sibling"} {
		t.Run(change, func(t *testing.T) {
			ctx := t.Context()
			f := seedRuntimeAppEnv(t, store)
			dep := f.deployments["stage"]
			if change == "sidecar-signal" {
				var err error
				dep, err = store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "stage", Kind: state.DeploymentKindImage,
					Sidecars: json.RawMessage(`[{"name":"helper","type":"sidecar"}]`)})
				if err != nil {
					t.Fatal(err)
				}
			}
			p := seedRuntimeInstancePublication(t, store, f, dep, node)
			var err error
			switch change {
			case "insert":
				err = store.UpsertAppEnvInScope(ctx, f.account.ID, f.app.ID, "stage", "NEW", "new")
			case "edit":
				err = store.UpsertAppEnvInScope(ctx, f.account.ID, f.app.ID, "stage", "MODE", "changed")
			case "delete":
				err = store.DeleteAppEnvInScope(ctx, f.account.ID, f.app.ID, "stage", "MODE")
			case "recreate":
				if err = store.DeleteAppEnvInScope(ctx, f.account.ID, f.app.ID, "stage", "MODE"); err == nil {
					err = store.UpsertAppEnvInScope(ctx, f.account.ID, f.app.ID, "stage", "MODE", "stage")
				}
			case "artifact":
				err = store.SetDeploymentRootfs(ctx, dep.ID, "/new.ext4", "apps/new.ext4", 4096)
			case "sidecar-layer":
				_, err = store.SetDeploymentSidecarLayer(ctx, state.DeploymentSidecarLayer{DeploymentID: dep.ID, SidecarName: "helper", StorageKey: "apps/helper.ext4", Bytes: 4096})
			case "sidecar-signal":
				err = store.SetDeploymentSidecarSecretReloadSignal(ctx, dep.ID, "helper", "SIGHUP")
			case "sibling":
				err = store.UpsertAppEnvInScope(ctx, f.account.ID, f.app.ID, "production", "MODE", "changed")
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.PublishOwnedInstanceRuntime(ctx, p)
			if change == "sibling" {
				if err != nil {
					t.Fatalf("production edit held stage publication: %v", err)
				}
			} else {
				if !errors.Is(err, state.ErrConflict) {
					t.Fatalf("changed captured config published: %v", err)
				}
				assertRuntimePublicationUnchanged(t.Context(), t, store, p)
				if _, err := store.InstanceRuntimeConfigFence(ctx, f.account.ID, f.app.ID, p.InstanceID); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("rejected publication saved a proof: %v", err)
				}
			}
		})
	}
}

func TestMemPausedRuntimeConfigProofCannotAdoptNewValues(t *testing.T) {
	testPausedRuntimeConfigProofCannotAdoptNewValues(t, state.NewMemStore())
}

func TestMemLegacyRuntimeConfigFenceProjection(t *testing.T) {
	testLegacyRuntimeConfigFenceProjection(t, state.NewMemStore())
}

func testLegacyRuntimeConfigFenceProjection(t *testing.T, store runtimeAppEnvTestStore) {
	ctx := t.Context()
	f := seedRuntimeAppEnv(t, store)
	app, err := store.CreateApp(ctx, state.App{AccountID: f.account.ID, Slug: "legacy-config-" + f.app.ID, Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, WarmPoolSize: 1, OnlyAllowDeclaredRoutes: true,
		DeclaredRoutes:  []state.DeclaredRoute{{Methods: []string{"GET"}, Path: "/ready"}},
		EgressAllowlist: []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}, EgressPorts: []int{443},
		PublicAuthIPAllowlist: []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")},
	})
	if err != nil {
		t.Fatal(err)
	}
	only, routes, ports := true, []state.DeclaredRoute{{Methods: []string{"GET"}, Path: "/ready"}}, []int{8443}
	retry := []byte(`{"max_attempts":3}`)
	app, err = store.UpdateApp(ctx, app.ID, state.UpdateAppParams{OnlyAllowDeclaredRoutes: &only, SetOnlyAllowDeclaredRoutes: true,
		DeclaredRoutes: &routes, SetDeclaredRoutes: true, EgressPorts: ports, SetEgressPorts: true, RetryPolicyJSON: &retry})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.RuntimeAppValuesForDeployment(ctx, app.AccountID, app.ID, dep.ID)
	if err != nil || !snapshot.MatchesRuntimeInputs(app, dep) || snapshot.Configuration.WorkloadSpecID != "" || snapshot.EnvironmentID != "" {
		t.Fatalf("legacy configuration projection rejected its actual boot inputs: %v", err)
	}
	f.app = app
	p := seedRuntimeInstancePublication(t, store, f, dep, runtimeSecretNodeForTest(t, store))
	target := 2
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{WarmPoolSize: &target, SetWarmPoolSize: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishOwnedInstanceRuntime(ctx, p); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("edited legacy settings published old captured inputs: %v", err)
	}
	assertRuntimePublicationUnchanged(t.Context(), t, store, p)
}

func TestMemFrozenRuntimeConfigFenceProjection(t *testing.T) {
	store := state.NewMemStore()
	testFrozenRuntimeConfigFenceProjection(t, store, func(_ *testing.T, id, raw string) { store.XFreezeDeploymentRuntime(id, raw) })
}

// A reviewed GitOps candidate boots from its frozen runtime. The app it
// resolves to on an ordinary wake must be the app its value snapshot hashed;
// otherwise every wake of the live candidate is refused with "deployment
// runtime value owner or grants changed". Ordinary deployment creation
// refuses a frozen runtime, so freeze stores it as candidate preparation does.
func testFrozenRuntimeConfigFenceProjection(t *testing.T, store runtimeAppEnvTestStore, freeze func(t *testing.T, deploymentID, raw string)) {
	f := seedRuntimeAppEnv(t, store)
	for _, workloadSpec := range []bool{true, false} {
		name := "legacy app"
		if workloadSpec {
			name = "environment workload spec"
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			app := f.app
			if !workloadSpec {
				var err error
				app, err = store.CreateApp(ctx, state.App{AccountID: f.account.ID, Slug: "frozen-legacy-" + uuid.NewString(),
					Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker, RAMMB: 256, MaxConcurrency: 2})
				if err != nil {
					t.Fatal(err)
				}
			}
			image := "registry.example/frozen@sha256:" + strings.Repeat("b", 64)
			// The seeded apps are workers, whose reviewed runtime must run as one.
			frozen := state.EnvironmentWorkloadRuntime{AppID: app.ID, AppType: state.AppTypeApp, WorkloadClass: app.WorkloadClass,
				Scope: "production", SourceID: uuid.NewString(), EnvironmentID: "environment", RevisionID: "revision", Generation: 1,
				Resource: "workload/api", PlanHash: strings.Repeat("a", 64), Source: &api.EnvironmentWorkloadSource{Kind: "image", Image: image},
				StartCommand: "serve --reviewed", Runtime: map[string]json.RawMessage{"execution_mode": json.RawMessage(`"worker"`)}}
			raw, err := json.Marshal(frozen)
			if err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage,
				ImageDigest: image})
			if err != nil {
				t.Fatal(err)
			}
			freeze(t, dep.ID, string(raw))
			if dep, err = store.DeploymentByID(ctx, dep.ID); err != nil || dep.EnvironmentWorkloadRuntime == "" {
				t.Fatalf("frozen runtime was not stored: %v", err)
			}
			resolved, err := state.ResolveAppForDeployment(ctx, store, app, dep)
			if err != nil {
				t.Fatal(err)
			}
			if resolved.StartCommand != frozen.StartCommand || resolved.Manifest.ExecutionMode != "worker" {
				t.Fatalf("resolved app ignored the frozen runtime: start=%q mode=%q", resolved.StartCommand, resolved.Manifest.ExecutionMode)
			}
			snapshot, err := store.RuntimeAppValuesForDeployment(ctx, app.AccountID, app.ID, dep.ID)
			if err != nil || !snapshot.MatchesRuntimeInputs(resolved, dep) {
				t.Fatalf("frozen runtime projection rejected its resolved boot inputs: %v", err)
			}
		})
	}
}

func testPausedRuntimeConfigProofCannotAdoptNewValues(t *testing.T, store runtimeAppEnvTestStore) {
	ctx := t.Context()
	f := seedRuntimeAppEnv(t, store)
	dep := f.deployments["stage"]
	p := seedRuntimeInstancePublication(t, store, f, dep, runtimeSecretNodeForTest(t, store))
	if err := store.UpdateInstanceState(ctx, p.InstanceID, string(state.StateWaking)); err != nil {
		t.Fatal(err)
	}
	p.ExpectedState, p.TargetState = string(state.StateWaking), string(state.StateWarm)
	if _, err := store.PublishOwnedInstanceRuntime(ctx, p); err != nil {
		t.Fatal(err)
	}
	assertProof := func() {
		t.Helper()
		proof, err := store.InstanceRuntimeConfigFence(ctx, f.account.ID, f.app.ID, p.InstanceID)
		if err != nil || proof != p.ConfigFence {
			t.Fatalf("stored capture changed: %v", err)
		}
	}
	assertProof()
	if err := store.UpsertAppEnvInScope(ctx, f.account.ID, f.app.ID, "stage", "MODE", "new-mode"); err != nil {
		t.Fatal(err)
	}
	assertProof()
	fresh, err := store.RuntimeAppValuesForDeployment(ctx, f.account.ID, f.app.ID, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	forged := p
	forged.ConfigFence, err = state.NewRuntimeAppConfigFence(fresh)
	if err != nil {
		t.Fatal(err)
	}
	forged.Fence = forged.ConfigFence.SecretFence
	forged.ExpectedState, forged.TargetState = string(state.StateWarm), string(state.StateRunning)
	if _, err := store.PublishOwnedInstanceRuntime(ctx, forged); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("paused VM adopted current values: %v", err)
	}
	row, err := store.InstanceByID(ctx, p.InstanceID)
	if err != nil || row.State != string(state.StateWarm) {
		t.Fatalf("failed resume changed state: %v", err)
	}
	assertProof()
	if err := store.DeleteInstance(ctx, p.InstanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InstanceRuntimeConfigFence(ctx, f.account.ID, f.app.ID, p.InstanceID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("proof survived physical row deletion: %v", err)
	}
}
