// adr: 1741 — a boot admitted from schedd's owner cache publishes on the
// app's runtime generation instead of the fence's reads, and writes nothing
// when the generation moved.
package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemGenerationGuardedPublication(t *testing.T) {
	testGenerationGuardedPublication(t, state.NewMemStore())
}

func testGenerationGuardedPublication(t *testing.T, store runtimeAppEnvTestStore) {
	generations := store.(state.AppRuntimeGenerationReader)
	f := seedRuntimeAppEnv(t, store)
	node := runtimeSecretNodeForTest(t, store)
	generation := func(t *testing.T) int64 {
		t.Helper()
		g, err := generations.AppRuntimeGeneration(t.Context(), f.app.ID)
		if err != nil {
			t.Fatal(err)
		}
		return g
	}

	t.Run("flag without the guard is refused", func(t *testing.T) {
		p := seedRuntimeInstancePublication(t, store, f, f.deployments["stage"], node)
		p.GenerationGuarded = true
		if _, err := store.PublishOwnedInstanceRuntime(t.Context(), p); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("guarded publication without a guard = %v, want ErrInvalidArgument", err)
		}
		assertRuntimePublicationUnchanged(t.Context(), t, store, p)
	})

	t.Run("moved generation writes nothing", func(t *testing.T) {
		p := seedRuntimeInstancePublication(t, store, f, f.deployments["stage"], node)
		p.GenerationGuarded = true
		ctx := state.WithRuntimeGenerationGuard(t.Context(), f.app.ID, generation(t))
		// A new deployment moves the generation but neither this
		// deployment's fence nor its plan, so only the generation can refuse
		// this publication.
		if _, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: f.app.ID, Scope: "production", Kind: state.DeploymentKindImage}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.PublishOwnedInstanceRuntime(ctx, p); !errors.Is(err, state.ErrRuntimeGenerationMoved) {
			t.Fatalf("publication on a moved generation = %v, want ErrRuntimeGenerationMoved", err)
		}
		assertRuntimePublicationUnchanged(t.Context(), t, store, p)
		// The same publication through the fence still succeeds.
		p.GenerationGuarded = false
		if published, err := store.PublishOwnedInstanceRuntime(ctx, p); err != nil || published.State != string(state.StateRunning) {
			t.Fatalf("fenced publication after the refusal = %+v, %v", published, err)
		}
	})

	t.Run("held generation publishes", func(t *testing.T) {
		p := seedRuntimeInstancePublication(t, store, f, f.deployments["stage"], node)
		p.GenerationGuarded = true
		ctx := state.WithRuntimeGenerationGuard(t.Context(), f.app.ID, generation(t))
		published, err := store.PublishOwnedInstanceRuntime(ctx, p)
		if err != nil || published.State != string(state.StateRunning) || published.Netns != p.Netns {
			t.Fatalf("guarded publication = %+v, %v; want RUNNING with its runtime", published, err)
		}
	})
}
