package routeadvisor

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func cacheRoute() RouteStats {
	return RouteStats{Method: "GET", Path: "/catalog", Requests: 1000, AnonymousRequests: 990, AnonymousSuccess: 980, ColdBoots: 40, CacheHits: 600, WakesAvoided: 25, P95LatencyMs: 80}
}

func baseInputs(routes ...RouteStats) Inputs {
	return Inputs{Routes: routes, Hosts: []string{"shop.gregale.dev", "shop.example.com"}, CacheMaxAgeSeconds: 60, AsyncAllowed: true, PlanMaxRPS: 100, PlanMaxBurst: 500}
}

func TestAdviseCache(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*RouteStats, *Inputs)
		wantKind string
	}{
		{"anonymous repeat GETs", func(*RouteStats, *Inputs) {}, api.RouteAdviceKindCache},
		{"too little traffic", func(r *RouteStats, _ *Inputs) { r.Requests = api.RouteAdviceMinRequests - 1 }, ""},
		{"identified callers", func(r *RouteStats, _ *Inputs) { r.AnonymousSuccess = 900 }, ""},
		{"low hit share", func(r *RouteStats, _ *Inputs) { r.CacheHits = 100 }, ""},
		{"templated path", func(r *RouteStats, _ *Inputs) { r.Path = "/catalog/{id}" }, ""},
		{"not GET", func(r *RouteStats, _ *Inputs) { r.Method = "PUT" }, ""},
		{"no host", func(_ *RouteStats, in *Inputs) { in.Hosts = nil }, ""},
		{"existing disabled cache rule", func(_ *RouteStats, in *Inputs) {
			in.Existing = []ExistingRule{{Kind: "cache", Path: "/catalog"}}
		}, ""},
		{"existing rule of another kind", func(_ *RouteStats, in *Inputs) {
			in.Existing = []ExistingRule{{Kind: "throttle", Path: "/*"}}
		}, api.RouteAdviceKindCache},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := cacheRoute()
			in := baseInputs()
			tt.mutate(&r, &in)
			in.Routes = []RouteStats{r}
			got := Advise(in)
			if tt.wantKind == "" {
				if len(got) != 0 {
					t.Fatalf("got %d suggestions, want none: %+v", len(got), got)
				}
				return
			}
			if len(got) != 1 || got[0].Kind != tt.wantKind {
				t.Fatalf("got %+v, want one %s suggestion", got, tt.wantKind)
			}
		})
	}
}

func TestCacheSuggestionRules(t *testing.T) {
	got := Advise(baseInputs(cacheRoute()))
	if len(got) != 1 {
		t.Fatalf("got %d suggestions, want 1", len(got))
	}
	s := got[0]
	if s.ID != SuggestionID("cache", "GET", "/catalog") || s.Impact.EstimatedCacheHits != 600 || s.Impact.EstimatedWakesAvoided != 25 {
		t.Fatalf("unexpected suggestion %+v", s)
	}
	if len(s.Rules) != 2 || s.Rules[0].MatchHost != "shop.gregale.dev" || s.Rules[1].MatchHost != "shop.example.com" {
		t.Fatalf("want one rule per host, got %+v", s.Rules)
	}
	for _, rule := range s.Rules {
		if rule.Enabled == nil || *rule.Enabled || rule.Kind != "cache" || rule.MatchPath != "/catalog" {
			t.Fatalf("rule must be a disabled cache rule on /catalog: %+v", rule)
		}
		var action api.EdgeRuleCacheAction
		if err := json.Unmarshal(rule.Action, &action); err != nil {
			t.Fatal(err)
		}
		if p := action.Validate(); p != nil {
			t.Fatalf("proposed cache action invalid: %v", p)
		}
		if action.MaxAgeSeconds != 60 {
			t.Fatalf("max_age_seconds = %d, want 60", action.MaxAgeSeconds)
		}
	}
}

func TestAdviseAsync(t *testing.T) {
	slow := RouteStats{Method: "POST", Path: "/reports/{id}/render", Requests: 400, Timeouts: 12, P95LatencyMs: 8000}
	tests := []struct {
		name   string
		mutate func(*RouteStats, *Inputs)
		want   bool
	}{
		{"timeouts", func(*RouteStats, *Inputs) {}, true},
		{"slow p95 without timeouts", func(r *RouteStats, _ *Inputs) { r.Timeouts, r.P95LatencyMs = 0, 12000 }, true},
		{"few timeouts", func(r *RouteStats, _ *Inputs) { r.Timeouts = api.RouteAdviceAsyncMinTimeouts - 1 }, false},
		{"GET is never made async", func(r *RouteStats, _ *Inputs) { r.Method = "GET" }, false},
		{"plan or app cannot run async", func(_ *RouteStats, in *Inputs) { in.AsyncAllowed = false }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := slow
			in := baseInputs()
			tt.mutate(&r, &in)
			in.Routes = []RouteStats{r}
			got := Advise(in)
			if !tt.want {
				if len(got) != 0 {
					t.Fatalf("want none, got %+v", got)
				}
				return
			}
			if len(got) != 1 || got[0].Kind != "async" || got[0].Rules[0].MatchPath != "/reports/*/render" {
				t.Fatalf("got %+v", got)
			}
			var action api.EdgeRuleAsyncAction
			if err := json.Unmarshal(got[0].Rules[0].Action, &action); err != nil || action.Validate() != nil {
				t.Fatalf("proposed async action invalid: %v", err)
			}
		})
	}
}

func throttleInputs() (Inputs, Key) {
	r := RouteStats{Method: "GET", Path: "/search", Requests: 10000, AnonymousRequests: 10}
	c := ConsumerStats{Method: "GET", Path: "/search", Consumers: 3,
		Top:  ConsumerPeak{ID: "c-top", Requests: 8000, PeakPerMinute: 900},
		Next: ConsumerPeak{ID: "c-next", Requests: 1500, PeakPerMinute: 60}}
	in := baseInputs(r)
	in.Consumers = []ConsumerStats{c}
	return in, Key{"GET", "/search"}
}

func TestThrottleCandidates(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Inputs)
		want   bool
	}{
		{"dominant consumer", func(*Inputs) {}, true},
		{"single consumer", func(in *Inputs) { in.Consumers[0].Consumers = 1 }, false},
		{"no dominant share", func(in *Inputs) { in.Consumers[0].Top.Requests = 4000 }, false},
		{"anonymous traffic would share the bucket", func(in *Inputs) { in.Routes[0].AnonymousRequests = 500 }, false},
		{"top peak below the limit", func(in *Inputs) { in.Consumers[0].Top.PeakPerMinute = 100 }, false},
		{"plan ceiling would throttle others", func(in *Inputs) { in.PlanMaxRPS = 1 }, false},
		{"already throttled", func(in *Inputs) { in.Existing = []ExistingRule{{Kind: "throttle", Path: "/*", Methods: []string{"GET"}}} }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in, key := throttleInputs()
			tt.mutate(&in)
			_, ok := ThrottleCandidates(in)[key]
			if ok != tt.want {
				t.Fatalf("candidate = %v, want %v", ok, tt.want)
			}
		})
	}
}

func TestThrottleSuggestionSizesLimitAboveOtherConsumers(t *testing.T) {
	in, key := throttleInputs()
	limit := ThrottleCandidates(in)[key]
	// Twice the next consumer's 60/min peak is 120/min, i.e. 2 req/s.
	if limit.RequestsPerSecond != 2 || limit.Burst != 20 || limit.AllowancePerMinute != 140 || limit.ConsumerID != "c-top" {
		t.Fatalf("unexpected limit %+v", limit)
	}
	in.ThrottleExcess = map[Key]int64{key: 5000}
	got := Advise(in)
	if len(got) != 1 || got[0].Kind != "throttle" || got[0].Impact.EstimatedThrottledRequests != 5000 || got[0].Evidence.TopConsumerID != "c-top" {
		t.Fatalf("got %+v", got)
	}
	var action api.EdgeRuleThrottleAction
	if err := json.Unmarshal(got[0].Rules[0].Action, &action); err != nil {
		t.Fatal(err)
	}
	if action.KeyBy != api.ThrottleKeyByConsumerID || action.RequestsPerSecond != 2 || action.Burst != 20 {
		t.Fatalf("unexpected action %+v", action)
	}
	in.ThrottleExcess = map[Key]int64{key: 50}
	if got := Advise(in); len(got) != 0 {
		t.Fatalf("excess below the minimum share must not suggest, got %+v", got)
	}
}

func TestAdviseOrdersByVolume(t *testing.T) {
	small := cacheRoute()
	small.Path, small.Requests, small.AnonymousSuccess, small.CacheHits = "/small", 300, 300, 200
	got := Advise(baseInputs(small, cacheRoute()))
	if len(got) != 2 || got[0].Route != "/catalog" || got[1].Route != "/small" {
		t.Fatalf("want /catalog first, got %+v", got)
	}
}

func TestPathGlob(t *testing.T) {
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"/users/{id}", "/users/*", true},
		{"/users/{id}/orders/{order}", "/users/*/orders/*", true},
		{"/static", "/static", true},
		{"/odd[1]", "", false},
		{"/a*b", "", false},
		{"relative", "", false},
	}
	for _, tt := range tests {
		got, ok := PathGlob(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("PathGlob(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}
