package state_test

// adr: 20261028110044

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// An enforce-mode route health gate holds a canary on sparse evidence. When
// the route health capability is not released for the account, the same
// advance passes and records no decision, history or notification.
func TestAdvanceCanaryIgnoresUnreleasedRouteHealth(t *testing.T) {
	_, s, a, app, d, seed := healthNotificationPG(t)
	seed(1, 100)
	var held api.RouteHealthDecision
	if _, _, err := s.AdvanceCanary(t.Context(), d.ID, state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 10, RouteHealthDecision: &held}); err == nil {
		t.Fatal("sparse evidence advanced a canary with the guard released")
	}
	before, err := s.ListRouteHealthHistory(t.Context(), a.ID, app.ID, d.ID, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := state.WithRouteGuardRelease(t.Context(), state.RouteGuardRelease{Health: false, Requirements: true})
	var decision api.RouteHealthDecision
	updated, _, err := s.AdvanceCanary(ctx, d.ID, state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 10, RouteHealthDecision: &decision})
	if err != nil || updated.TrafficPercent != 10 {
		t.Fatalf("advance with the guard unreleased: traffic=%d err=%v, want 10%%", updated.TrafficPercent, err)
	}
	if decision != (api.RouteHealthDecision{}) {
		t.Fatalf("unreleased guard made a decision: %+v", decision)
	}
	after, err := s.ListRouteHealthHistory(t.Context(), a.ID, app.ID, d.ID, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Entries) != len(before.Entries) {
		t.Fatalf("history entries %d -> %d; an unreleased guard records nothing", len(before.Entries), len(after.Entries))
	}
}
