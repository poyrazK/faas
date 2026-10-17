package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestServiceBindingPlanMem(t *testing.T) { serviceBindingPlanSuite(t, state.NewMemStore()) }
func TestServiceBindingPlanPG(t *testing.T) {
	store, _ := pgStore(t)
	serviceBindingPlanSuite(t, store)
}

func serviceBindingPlanSuite(t *testing.T, store state.Store) {
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@serviceplan.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "plan-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, Manifest: state.AppManifest{ServiceBindings: api.ServiceBindingsForTargets([]string{"billing"}), Env: map[string]string{"CUSTOM": "original"}}})
	if err != nil {
		t.Fatal(err)
	}
	expected := state.ServiceBindingFingerprint(app)
	// An unrelated manifest edit must survive a guarded update built earlier.
	unrelated := app.Manifest
	unrelated.Env = map[string]string{"CUSTOM": "concurrent"}
	app, err = store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &unrelated})
	if err != nil {
		t.Fatal(err)
	}
	if state.ServiceBindingFingerprint(app) != expected {
		t.Fatal("unrelated setting invalidated fingerprint")
	}
	staleManifest := unrelated
	staleManifest.Env = map[string]string{"CUSTOM": "stale"}
	staleManifest.ServiceBindingTransport = api.ServiceBindingTransportHTTPS
	saved, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &staleManifest, ExpectedServiceBindingFingerprint: expected})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Manifest.Env["CUSTOM"] != "concurrent" || saved.Manifest.Env[api.ServiceBindingEnvKey("billing")] != "https://billing.internal" {
		t.Fatalf("lost concurrent env: %v", saved.Manifest.Env)
	}
	if state.ServiceBindingFingerprint(saved) == expected {
		t.Fatal("transport change did not change fingerprint")
	}
	// A stale request must change neither the manifest nor other columns.
	ram := 256
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &staleManifest, RAMMB: &ram, ExpectedServiceBindingFingerprint: expected}); !errors.Is(err, state.ErrServiceBindingPlanConflict) {
		t.Fatalf("stale err=%v", err)
	}
	current, err := store.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.RAMMB != 128 || state.ServiceBindingFingerprint(current) != state.ServiceBindingFingerprint(saved) {
		t.Fatal("stale update mutated app")
	}
	if activity, ok := store.(state.OrgActivityAppConfigMutationStore); ok {
		called := false
		_, _, err := activity.UpdateAppWithActivity(ctx, app.ID, state.UpdateAppParams{Manifest: &staleManifest, ExpectedServiceBindingFingerprint: expected}, state.OrgActivity{}, func(before, after state.App) (json.RawMessage, bool, error) { called = true; return nil, false, nil })
		if !errors.Is(err, state.ErrServiceBindingPlanConflict) || called {
			t.Fatalf("stale activity err=%v callback=%v", err, called)
		}
	}
	// A reliability edit after review must block a stale target-removal
	// plan, rather than pruning the newly saved override.
	reviewed := current
	revised := current.Manifest
	revised.ServiceReliability = map[string]api.ServiceReliabilityPolicy{"billing": {TimeoutMS: 100}}
	if _, err := store.UpdateApp(ctx, current.ID, state.UpdateAppParams{Manifest: &revised}); err != nil {
		t.Fatal(err)
	}
	removal := reviewed.Manifest
	removal.ServiceBindings = nil
	removal.ServiceReliability = nil
	if _, err := store.UpdateApp(ctx, current.ID, state.UpdateAppParams{Manifest: &removal, ExpectedServiceBindingFingerprint: state.ServiceBindingFingerprint(reviewed)}); !errors.Is(err, state.ErrServiceBindingPlanConflict) {
		t.Fatalf("stale pruning err=%v", err)
	}
	current, err = store.AppByID(ctx, current.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Manifest.ServiceBindings) != 1 || current.Manifest.ServiceReliability["billing"].TimeoutMS != 100 {
		t.Fatal("stale plan pruned a concurrent override")
	}
	// Competing plans from the same snapshot have exactly one winner.
	expected = state.ServiceBindingFingerprint(current)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, policy := range []api.ServiceBindingPolicy{api.ServiceBindingPolicyDeclared, api.ServiceBindingPolicyAccount} {
		wg.Go(func() {
			m := current.Manifest
			m.ServiceBindingPolicy = policy
			m.AllowedServiceCallers = &[]string{}
			_, err := store.UpdateApp(ctx, current.ID, state.UpdateAppParams{Manifest: &m, ExpectedServiceBindingFingerprint: expected})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, state.ErrServiceBindingPlanConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("winners=%d conflicts=%d", successes, conflicts)
	}
}
