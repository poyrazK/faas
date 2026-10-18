package outbound

// adr: 1434 — a route stores its strategy, weights and latency target; the
// route query reads a latency or cost route's window counts; settlements
// count those routes' attempts without a canary; and new targets reset them.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresRouteStrategies(t *testing.T) {
	f := newAIPreviewFixture(t, okChatCompletion, state.OutboundAIPolicy{})
	ctx := context.Background()
	second := f.secondAIIntegration(api.OutboundAIProviderOpenAI)
	backend, err := NewPostgresBackend(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewPostgresRouteResolver(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	weight := func(value int) *int { return &value }
	if _, err := f.store.CreateOutboundAIRoute(ctx, state.OutboundAIRoute{ID: uuid.NewString(), AccountID: f.account.ID, Name: "zero",
		Strategy: api.OutboundAIRouteStrategyWeighted, Targets: []state.OutboundAIRouteTarget{
			{IntegrationID: f.offer.ID, Weight: weight(0)}, {IntegrationID: second.ID, Weight: weight(0)}}}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("a weighted route without weight = %v", err)
	}
	route, err := f.store.CreateOutboundAIRoute(ctx, state.OutboundAIRoute{ID: uuid.NewString(), AccountID: f.account.ID, Name: "split",
		Strategy: api.OutboundAIRouteStrategyWeighted, Targets: []state.OutboundAIRouteTarget{
			{IntegrationID: f.offer.ID, Weight: weight(70)}, {IntegrationID: second.ID, Model: "gpt-new", Weight: weight(30)}}})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := resolver.Route(ctx, route.ID)
	if err != nil || loaded.Strategy != api.OutboundAIRouteStrategyWeighted || !equalInts(loaded.Weights, []int{70, 30}) ||
		loaded.MaxTTFBMS != nil || loaded.Targets[1].Model != "gpt-new" {
		t.Fatalf("resolved weighted route = %+v, %v", loaded, err)
	}
	// A weighted route without a canary counts nothing.
	if _, err := backend.RecordAIUsageAlerts(ctx, routeAttemptSettlement(f, route.ID, 1, false)); err != nil {
		t.Fatal(err)
	}
	assertRouteRows(t, f, route.ID, 0)

	maxTTFB := 800
	plainTargets := []state.OutboundAIRouteTarget{{IntegrationID: f.offer.ID}, {IntegrationID: second.ID}}
	if err := f.store.SetOutboundAIRouteTargets(ctx, f.account.ID, route.ID, plainTargets,
		state.OutboundAIRouteRouting{Strategy: api.OutboundAIRouteStrategyLatency, MaxTTFBMS: &maxTTFB}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("a latency target on a latency route = %v", err)
	}
	if err := f.store.SetOutboundAIRouteTargets(ctx, f.account.ID, route.ID, plainTargets,
		state.OutboundAIRouteRouting{Strategy: api.OutboundAIRouteStrategyCost, MaxTTFBMS: &maxTTFB}); err != nil {
		t.Fatal(err)
	}
	// The first target fails a third of its attempts; the second none.
	for index := range 12 {
		if _, err := backend.RecordAIUsageAlerts(ctx, routeAttemptSettlement(f, route.ID, 1, index%3 == 0)); err != nil {
			t.Fatal(err)
		}
		if _, err := backend.RecordAIUsageAlerts(ctx, routeAttemptSettlement(f, route.ID, 2, false)); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err = resolver.Route(ctx, route.ID)
	if err != nil || loaded.Strategy != api.OutboundAIRouteStrategyCost || loaded.MaxTTFBMS == nil || *loaded.MaxTTFBMS != 800 ||
		!equalInts(loaded.Weights, []int{0, 0}) || len(loaded.Stats) != 2 ||
		loaded.Stats[0] != (AIRouteTargetCounts{Calls: 12, Errors: 4, TTFBCalls: 8, TTFBMSTotal: 800, CostMillicents: 24}) ||
		loaded.Stats[1] != (AIRouteTargetCounts{Calls: 12, TTFBCalls: 12, TTFBMSTotal: 2400, CostMillicents: 36}) {
		t.Fatalf("resolved cost route = %+v, %v", loaded, err)
	}
	plain := httptest.NewRequest(http.MethodPost, "/", nil)
	if steps, pick := planRouteSteps(loaded, plain, false, func(n int) int { return n - 1 }); !equalInts(positions(steps), []int{2, 1}) ||
		pick != routePickBest {
		t.Fatalf("cost route order = %v (%s)", positions(steps), pick)
	}
	window, err := f.store.OutboundAIRouteStats(ctx, f.account.ID, route.ID)
	if err != nil || window.Strategy != api.OutboundAIRouteStrategyCost || len(window.Targets) != 2 ||
		window.Targets[0] != (state.OutboundAIRouteTargetStats{Position: 1, Calls: 12, Errors: 4, TTFBCalls: 8, TTFBMSTotal: 800, CostMillicents: 24}) ||
		window.Targets[1].Calls != 12 || window.WindowStart.IsZero() {
		t.Fatalf("OutboundAIRouteStats = %+v, %v", window, err)
	}
	if _, err := f.store.OutboundAIRouteStats(ctx, uuid.NewString(), route.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("another account's route stats = %v", err)
	}

	// The database refuses what the store would.
	if _, err := f.pool.Exec(ctx, `UPDATE outbound_ai_route_targets SET weight = 101 WHERE route_id = $1`, route.ID); err == nil {
		t.Fatal("a weight above 100 was stored")
	}
	if _, err := f.pool.Exec(ctx, `UPDATE outbound_ai_routes SET strategy = 'latency' WHERE id = $1`, route.ID); err == nil {
		t.Fatal("a latency target was stored on a latency route")
	}

	// New targets reset the numbers.
	if err := f.store.SetOutboundAIRouteTargets(ctx, f.account.ID, route.ID, []state.OutboundAIRouteTarget{plainTargets[1], plainTargets[0]},
		state.OutboundAIRouteRouting{Strategy: api.OutboundAIRouteStrategyLatency}); err != nil {
		t.Fatal(err)
	}
	assertRouteRows(t, f, route.ID, 0)
	if loaded, err := resolver.Route(ctx, route.ID); err != nil || loaded.MaxTTFBMS != nil || loaded.Stats[0] != (AIRouteTargetCounts{}) {
		t.Fatalf("route after new targets = %+v, %v", loaded, err)
	}

	// A latency route keeps counting after its canary rolls back; the
	// canary's counts stop at the minute it was decided.
	if _, err := f.store.SetOutboundAIRouteCanary(ctx, f.account.ID, route.ID, state.OutboundAIRouteCanary{Position: 2, Percent: 25,
		MaxErrorRateIncrease: 5, MinCalls: 10, WindowMinutes: 30}); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := backend.RecordAIUsageAlerts(ctx, routeAttemptSettlement(f, route.ID, 2, false)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.pool.Exec(ctx, `
		UPDATE outbound_ai_route_canaries SET state = 'rolled_back', reason = 'error_rate', started_at = now() - interval '10 minutes',
		       decided_at = now() - interval '5 minutes' WHERE route_id = $1`, route.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO outbound_ai_route_target_minutes (route_id, position, minute, calls, errors, ttfb_calls, ttfb_ms_total, cost_millicents)
		VALUES ($1, 2, date_trunc('minute', now() - interval '7 minutes'), 4, 1, 3, 600, 9)`, route.ID); err != nil {
		t.Fatal(err)
	}
	status, err := f.store.OutboundAIRouteCanaryStatus(ctx, f.account.ID, route.ID)
	if err != nil || status.CanaryStats.Calls != 4 || status.CanaryStats.Errors != 1 {
		t.Fatalf("a rolled-back canary's counts = %+v, %v", status.CanaryStats, err)
	}
	if window, err := f.store.OutboundAIRouteStats(ctx, f.account.ID, route.ID); err != nil || window.Targets[1].Calls != 7 {
		t.Fatalf("route stats after a rollback = %+v, %v", window, err)
	}
}
