package conformance

import (
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-464: two ordinary allocations can observe the same safe hint, but the
// second committed write must not consume selected-region recovery capacity.
func testOrdinaryCapacityStaleHint(t *testing.T, fx *Fixture) {
	fleet := regionalCapacityFleet(t, fx, 2, 2)
	service := regionalCapacityApp(t, fx, "eu-fsn", 3)
	regionalCapacityDeployment(t, fx, service)
	if _, err := fx.Store.SetServiceCapacityProtection(fx.Ctx, true); err != nil {
		t.Fatal(err)
	}
	hint, err := fx.Store.ServiceCapacityPlacement(fx.Ctx)
	if err != nil || !hint.AllowsOrdinary(fleet["eu-fsn"][0].ID, 520, 1000, 2) {
		t.Fatalf("safe ordinary hint: %+v %v", hint, err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, err := fx.Store.CreateInstance(fx.Ctx, fx.App.ID, fx.Deployment.ID, string(state.StateRunning), 512, fleet["eu-fsn"][0].ID, uuid.NewString())
			results <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	accepted, refused := 0, 0
	for err := range results {
		switch {
		case err == nil:
			accepted++
		case state.ServiceCapacityProblem(err) != nil:
			refused++
		default:
			t.Fatalf("unexpected ordinary admission error: %v", err)
		}
	}
	if accepted != 1 || refused != 1 {
		t.Fatalf("stale hints consumed recovery: accepted=%d refused=%d", accepted, refused)
	}
	fresh, err := fx.Store.ServiceCapacityPlacement(fx.Ctx)
	if err != nil || fresh.AllowsOrdinary(fleet["eu-fsn"][0].ID, 520, 1000, 2) || !hint.AllowsOrdinary(fleet["eu-fsn"][0].ID, 520, 1000, 2) {
		t.Fatalf("ordinary hints are not immutable snapshots: fresh=%+v %v", fresh, err)
	}
}
