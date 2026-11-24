package state_test

// adr: 20261123192254 — per-route consumer plan limits (MCP tool caps).

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDecideRouteAdmissionWindows(t *testing.T) {
	now := time.Date(2026, 11, 30, 23, 58, 30, 0, time.UTC)
	limit := state.APIConsumerPlanRouteLimit{MaxRequestsPerMinute: 2, MaxRequestsPerMonth: 3}
	var counter state.PlanAdmissionCounter
	var decision state.APIConsumerPlanDecision
	for i := range 2 {
		if counter, decision = state.DecideRouteAdmission(counter, limit, now); !decision.Allowed {
			t.Fatalf("request %d denied: %+v", i, decision)
		}
	}
	if _, decision = state.DecideRouteAdmission(counter, limit, now); decision.Allowed || decision.Scope != "route_minute" || decision.Limit != 2 || decision.RetryAfterSeconds != 30 {
		t.Fatalf("third request in the minute = %+v", decision)
	}
	nextMinute := now.Add(time.Minute)
	if counter, decision = state.DecideRouteAdmission(counter, limit, nextMinute); !decision.Allowed || counter.MonthUsed != 3 || counter.MinuteUsed != 1 {
		t.Fatalf("third request in the month = %+v %+v", counter, decision)
	}
	if _, decision = state.DecideRouteAdmission(counter, limit, nextMinute); decision.Allowed || decision.Scope != "route_month" || decision.Observed != 3 {
		t.Fatalf("fourth request in the month = %+v", decision)
	}
	if counter, decision = state.DecideRouteAdmission(counter, limit, nextMinute.Add(time.Minute)); !decision.Allowed || counter.MonthUsed != 1 {
		t.Fatalf("new month = %+v %+v, want reset", counter, decision)
	}
}

func TestValidateAPIConsumerPlanRouteLimits(t *testing.T) {
	ok := state.APIConsumerPlanRouteLimit{MaxRequestsPerMonth: 10}
	for _, tc := range []struct {
		name   string
		limits map[string]state.APIConsumerPlanRouteLimit
		ok     bool
	}{
		{"none", nil, true},
		{"mcp tool", map[string]state.APIConsumerPlanRouteLimit{"MCP /tools/search": ok}, true},
		{"http route", map[string]state.APIConsumerPlanRouteLimit{"POST /v1/generate": {MaxRequestsPerMinute: 5}}, true},
		{"not a route", map[string]state.APIConsumerPlanRouteLimit{"search": ok}, false},
		{"no limit", map[string]state.APIConsumerPlanRouteLimit{"MCP /tools/search": {}}, false},
		{"negative", map[string]state.APIConsumerPlanRouteLimit{"MCP /tools/search": {MaxRequestsPerMinute: -1, MaxRequestsPerMonth: 5}}, false},
	} {
		if err := state.ValidateAPIConsumerPlanRouteLimits(tc.limits); (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
	tooMany := map[string]state.APIConsumerPlanRouteLimit{}
	for i := range state.MaxAPIConsumerPlanRouteLimits + 1 {
		tooMany["MCP /tools/t"+uuid.NewString()[:8]+string(rune('a'+i%26))] = ok
	}
	if state.ValidateAPIConsumerPlanRouteLimits(tooMany) == nil {
		t.Error("more than the maximum route limits must be refused")
	}
}

func TestMemConsumerPlanRouteLimits(t *testing.T) {
	m := state.NewMemStore()
	ctx := context.Background()
	accountID, appID := uuid.NewString(), uuid.NewString()
	consumer, err := m.CreateAPIConsumer(ctx, accountID, appID, "cust-1", "Customer One")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := m.CreateAPIConsumerPlan(ctx, state.APIConsumerPlan{AccountID: accountID, AppID: appID, Name: "free",
		MaxUnitsPerMonth: 100, RouteLimits: map[string]state.APIConsumerPlanRouteLimit{"MCP /tools/generate": {MaxRequestsPerMonth: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.AssignAPIConsumerPlan(ctx, state.APIConsumerPlanAssignment{AccountID: accountID, AppID: appID, ConsumerID: consumer.ID,
		PlanID: plan.ID, EffectiveFrom: time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	policy, err := m.GetAPIConsumerPlanPolicy(ctx, accountID, appID, consumer.ID, time.Now().UTC())
	if err != nil || policy.RouteLimits["MCP /tools/generate"].MaxRequestsPerMonth != 2 || !policy.Limited() {
		t.Fatalf("policy = %+v err=%v", policy, err)
	}
	admit := func(route string) state.APIConsumerPlanDecision {
		t.Helper()
		decision, err := m.AdmitAPIConsumerPlanRequest(ctx, accountID, consumer.ID, policy, route, 1)
		if err != nil {
			t.Fatal(err)
		}
		return decision
	}
	for range 2 {
		if d := admit("MCP /tools/generate"); !d.Allowed {
			t.Fatalf("capped tool denied early: %+v", d)
		}
	}
	if d := admit("MCP /tools/generate"); d.Allowed || d.Scope != "route_month" || d.Limit != 2 || d.Observed != 2 {
		t.Fatalf("third call = %+v, want route_month denial", d)
	}
	if d := admit("MCP /tools/search"); !d.Allowed {
		t.Fatalf("an uncapped tool must stay admitted: %+v", d)
	}

	updated, err := m.UpdateAPIConsumerPlanLimits(ctx, accountID, appID, plan.ID, 0, 100, nil, nil)
	if err != nil || len(updated.RouteLimits) != 1 {
		t.Fatalf("nil route limits must keep them: %+v err=%v", updated.RouteLimits, err)
	}
	if _, err := m.UpdateAPIConsumerPlanLimits(ctx, accountID, appID, plan.ID, 0, 100, nil,
		map[string]state.APIConsumerPlanRouteLimit{"bad": {MaxRequestsPerMonth: 1}}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("invalid route limits err = %v", err)
	}
	cleared, err := m.UpdateAPIConsumerPlanLimits(ctx, accountID, appID, plan.ID, 0, 100, nil, map[string]state.APIConsumerPlanRouteLimit{})
	if err != nil || len(cleared.RouteLimits) != 0 || cleared.RouteLimits == nil {
		t.Fatalf("empty route limits must clear them: %+v err=%v", cleared.RouteLimits, err)
	}
}
