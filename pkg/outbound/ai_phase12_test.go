package outbound

// adr: 1434 — a route's strategy picks the target each call tries first:
// by weight (sticky per session or user), by lowest recent time to first
// byte, or by lowest recent cost within a latency target; targets without
// data are explored and unhealthy ones tried last.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

func routeOf(strategy string, targets int) AIRoute {
	route := AIRoute{ID: "route-" + strategy, Strategy: strategy}
	for index := range targets {
		route.Targets = append(route.Targets, AIRouteTarget{IntegrationID: string(rune('a' + index))})
	}
	return route
}

func positions(steps []routeStep) []int {
	out := make([]int, 0, len(steps))
	for _, step := range steps {
		out = append(out, step.position)
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}

func TestWeightedRoutesDrawByWeight(t *testing.T) {
	route := routeOf(api.OutboundAIRouteStrategyWeighted, 3)
	route.Weights = []int{70, 0, 30}
	plain := httptest.NewRequest(http.MethodPost, "/", nil)
	firsts := map[int]int{}
	for draw := range 100 {
		steps, pick := planRouteSteps(route, plain, false, func(n int) int { return draw % n })
		if pick != routePickWeighted {
			t.Fatalf("pick = %s", pick)
		}
		firsts[steps[0].position]++
		if steps[0].position == 1 && !equalInts(positions(steps), []int{1, 2, 3}) ||
			steps[0].position == 3 && !equalInts(positions(steps), []int{3, 1, 2}) {
			t.Fatalf("fallbacks = %v", positions(steps))
		}
	}
	if firsts[1] != 70 || firsts[2] != 0 || firsts[3] != 30 {
		t.Fatalf("first targets = %v", firsts)
	}
	session := httptest.NewRequest(http.MethodPost, "/", nil)
	session.Header.Set(api.OutboundAISessionHeader, "conversation-7")
	first, _ := planRouteSteps(route, session, false, func(int) int { return 0 })
	for draw := range 20 {
		again, _ := planRouteSteps(route, session, false, func(n int) int { return (draw * 7) % n })
		if again[0].position != first[0].position {
			t.Fatal("a session moved between targets")
		}
	}
	route.Weights = nil
	if steps, pick := planRouteSteps(route, plain, false, func(int) int { return 0 }); pick != routePickOrdered || steps[0].position != 1 {
		t.Fatalf("a weighted route without weights = %v, %s", positions(steps), pick)
	}
}

func TestJudgedRoutesOrderByStats(t *testing.T) {
	plain := httptest.NewRequest(http.MethodPost, "/", nil)
	never := func(n int) int { return n - 1 } // never below the explore share
	latency := routeOf(api.OutboundAIRouteStrategyLatency, 5)
	latency.Stats = []AIRouteTargetCounts{
		{Calls: 50, Errors: 1, TTFBCalls: 49, TTFBMSTotal: 49 * 900}, // healthy, 900 ms
		{Calls: 5, TTFBCalls: 5, TTFBMSTotal: 5 * 100},               // too few calls
		{Calls: 40, Errors: 20, TTFBCalls: 20, TTFBMSTotal: 20 * 50}, // unhealthy
		{Calls: 30, TTFBCalls: 30, TTFBMSTotal: 30 * 300},            // healthy, 300 ms
		{}, // no data
	}
	steps, pick := planRouteSteps(latency, plain, false, never)
	if want := []int{4, 1, 2, 5, 3}; !equalInts(positions(steps), want) || pick != routePickBest {
		t.Fatalf("latency order = %v (%s), want %v", positions(steps), pick, want)
	}
	explore := func(n int) int { return 0 } // below the explore share; the first unknown target
	if steps, pick := planRouteSteps(latency, plain, false, explore); steps[0].position != 2 || pick != routePickExplore {
		t.Fatalf("explored = %v (%s)", positions(steps), pick)
	}

	cost := routeOf(api.OutboundAIRouteStrategyCost, 3)
	maxTTFB := 500
	cost.MaxTTFBMS = &maxTTFB
	cost.Stats = []AIRouteTargetCounts{
		{Calls: 20, TTFBCalls: 20, TTFBMSTotal: 20 * 400, CostMillicents: 20 * 90}, // 90 per call, fast enough
		{Calls: 20, TTFBCalls: 20, TTFBMSTotal: 20 * 800, CostMillicents: 20 * 10}, // cheapest, too slow
		{Calls: 20, Errors: 2, TTFBCalls: 18, TTFBMSTotal: 18 * 300, CostMillicents: 18 * 40},
	}
	if steps, _ := planRouteSteps(cost, plain, false, never); !equalInts(positions(steps), []int{3, 1, 2}) {
		t.Fatalf("cost order = %v", positions(steps))
	}
	cost.MaxTTFBMS = nil
	if steps, _ := planRouteSteps(cost, plain, false, never); !equalInts(positions(steps), []int{2, 3, 1}) {
		t.Fatalf("cost order without a latency target = %v", positions(steps))
	}

	cold := routeOf(api.OutboundAIRouteStrategyLatency, 3)
	if steps, pick := planRouteSteps(cold, plain, false, never); !equalInts(positions(steps), []int{1, 2, 3}) || pick != routePickOrdered {
		t.Fatalf("a cold route = %v (%s)", positions(steps), pick)
	}
	latency.Canary = &AIRouteCanary{Position: 5, Percent: 10}
	if steps, pick := planRouteSteps(latency, plain, true, never); !equalInts(positions(steps), []int{5, 4, 1, 2, 3}) || pick != routePickCanary {
		t.Fatalf("a canary on a latency route = %v (%s)", positions(steps), pick)
	}
	if !routeTracksStats(routeOf(api.OutboundAIRouteStrategyCost, 1)) || routeTracksStats(routeOf(api.OutboundAIRouteStrategyWeighted, 1)) {
		t.Fatal("routeTracksStats")
	}
}

func TestValidOutboundAIRouteStrategy(t *testing.T) {
	weight := func(values ...int) []api.OutboundAIRouteTarget {
		out := make([]api.OutboundAIRouteTarget, 0, len(values))
		for _, value := range values {
			out = append(out, api.OutboundAIRouteTarget{IntegrationID: "x", Weight: &value})
		}
		return out
	}
	plain := []api.OutboundAIRouteTarget{{IntegrationID: "x"}, {IntegrationID: "y"}}
	ms := func(value int) *int { return &value }
	for _, tc := range []struct {
		name     string
		strategy string
		maxTTFB  *int
		targets  []api.OutboundAIRouteTarget
		want     bool
	}{
		{"default", "", nil, plain, true},
		{"latency", api.OutboundAIRouteStrategyLatency, nil, plain, true},
		{"cost with a latency target", api.OutboundAIRouteStrategyCost, ms(800), plain, true},
		{"weighted", api.OutboundAIRouteStrategyWeighted, nil, weight(70, 30), true},
		{"weighted fallback only", api.OutboundAIRouteStrategyWeighted, nil, weight(100, 0), true},
		{"all zero weights", api.OutboundAIRouteStrategyWeighted, nil, weight(0, 0), false},
		{"missing weight", api.OutboundAIRouteStrategyWeighted, nil, append(weight(50), api.OutboundAIRouteTarget{IntegrationID: "y"}), false},
		{"weight over 100", api.OutboundAIRouteStrategyWeighted, nil, weight(101), false},
		{"weights on ordered", api.OutboundAIRouteStrategyOrdered, nil, weight(50, 50), false},
		{"latency target on latency", api.OutboundAIRouteStrategyLatency, ms(800), plain, false},
		{"latency target out of range", api.OutboundAIRouteStrategyCost, ms(600_001), plain, false},
		{"unknown strategy", "random", nil, plain, false},
	} {
		if got := api.ValidOutboundAIRouteStrategy(tc.strategy, tc.maxTTFB, tc.targets); got != tc.want {
			t.Errorf("%s = %v", tc.name, got)
		}
	}
}

func TestLatencyRouteServesTheFastestTarget(t *testing.T) {
	var mu sync.Mutex
	var served []string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		served = append(served, r.Host)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"gpt-5","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`)
	}))
	defer upstream.Close()
	account, app := uuid.NewString(), uuid.NewString()
	slow := phase9Integration(t, "slow", upstream.URL, api.OutboundAIProviderOpenAI, account, app)
	fast := phase9Integration(t, "fast", upstream.URL, api.OutboundAIProviderOpenAI, account, app)
	handler, backend := phase9Handler(t, upstream.Client(), slow, fast)
	handler.routeRandom = func(n int) int { return n - 1 }
	routeID := uuid.NewString()
	handler.Routes = StaticRouteResolver{routeID: {ID: routeID, AccountID: account, Strategy: api.OutboundAIRouteStrategyLatency,
		Targets: []AIRouteTarget{{IntegrationID: "slow"}, {IntegrationID: "fast"}},
		Stats:   []AIRouteTargetCounts{{Calls: 20, TTFBCalls: 20, TTFBMSTotal: 20 * 900}, {Calls: 20, TTFBCalls: 20, TTFBMSTotal: 20 * 200}}}}
	token := AIAccessToken{ID: uuid.NewString(), AccountID: account, AppID: app, ExpiresAt: time.Now().Add(time.Hour)}
	got := asApp(handler, http.MethodPost, "/r/"+routeID+"/v1/chat/completions", `{"model":"gpt-5","messages":[]}`, token)
	if got.Code != http.StatusOK || got.Header().Get(RouteTargetHeader) != "fast" {
		t.Fatalf("latency route = %d %s, served by %q", got.Code, got.Body.String(), got.Header().Get(RouteTargetHeader))
	}
	attempts := backend.AIRouteAttempts()
	if len(attempts) != 1 || attempts[0].RouteID != routeID || attempts[0].Position != 2 || attempts[0].Error {
		t.Fatalf("tracked attempts = %+v", attempts)
	}
	if !strings.Contains(strings.Join(served, ","), strings.TrimPrefix(upstream.URL, "https://")) {
		t.Fatalf("upstream calls = %v", served)
	}
}
