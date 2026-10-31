// adr: 051
package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemRecordObservedWorkloadClass(t *testing.T) {
	testRecordObservedWorkloadClass(t, state.NewMemStore())
}

// A cold boot publishes its config proof before it knows the workload class it
// will observe, and the class is part of the configuration the proof covers.
// Recording the class moves the booted instance's proof with it, but only when
// nothing else changed since the publication.
func testRecordObservedWorkloadClass(t *testing.T, store runtimeAppEnvTestStore) {
	node := runtimeSecretNodeForTest(t, store)
	for _, tc := range []struct {
		name      string
		published bool
		edited    bool // another configuration change after the publication
		class     state.WorkloadClass
		restamped bool
	}{
		{name: "only the class changed", published: true, class: state.WorkloadClassWorker, restamped: true},
		{name: "same class", published: true, class: state.WorkloadClassHTTP},
		{name: "configuration edited since the publication", published: true, edited: true, class: state.WorkloadClassWorker},
		{name: "no proof", class: state.WorkloadClassWorker},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			f := seedRuntimeAppEnv(t, store)
			app, err := store.CreateApp(ctx, state.App{AccountID: f.account.ID, Slug: "observed-class-" + f.app.ID, Type: state.AppTypeApp,
				WorkloadClass: state.WorkloadClassHTTP, RAMMB: 256, MaxConcurrency: 2})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage})
			if err != nil {
				t.Fatal(err)
			}
			f.app = app
			p := seedRuntimeInstancePublication(t, store, f, dep, node)
			if tc.published {
				if _, err := store.PublishOwnedInstanceRuntime(ctx, p); err != nil {
					t.Fatal(err)
				}
			}
			if tc.edited {
				if err := store.UpsertAppEnvInScope(ctx, f.account.ID, app.ID, "default", "MODE", "changed"); err != nil {
					t.Fatal(err)
				}
			}

			restamped, err := store.RecordObservedWorkloadClass(ctx, f.account.ID, app.ID, p.InstanceID, tc.class)
			if err != nil || restamped != tc.restamped {
				t.Fatalf("RecordObservedWorkloadClass = %v, %v; want %v", restamped, err, tc.restamped)
			}
			if got, err := store.AppByID(ctx, app.ID); err != nil || got.WorkloadClass != tc.class {
				t.Fatalf("app class = %q, %v; want %q", got.WorkloadClass, err, tc.class)
			}
			proof, err := store.InstanceRuntimeConfigFence(ctx, f.account.ID, app.ID, p.InstanceID)
			if !tc.published {
				if !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("an unpublished instance has a proof: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := store.RuntimeAppValuesForDeployment(ctx, f.account.ID, app.ID, dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			current, err := state.NewRuntimeAppConfigFence(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case tc.restamped && (proof != current || proof == p.ConfigFence):
				t.Fatal("the proof does not match the configuration with the observed class")
			case !tc.restamped && proof != p.ConfigFence:
				t.Fatal("the proof moved although more than the class changed, or nothing did")
			case tc.edited && proof == current:
				t.Fatal("recording the class let the proof adopt an unrelated configuration change")
			}
		})
	}
}
