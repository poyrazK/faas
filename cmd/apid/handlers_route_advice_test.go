package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type routeAdviceTestStore struct {
	*state.MemStore
	stats       sqlc.RouteAdviceRouteStatsParams
	excess      []sqlc.RouteAdviceThrottleExcessParams
	statRows    []sqlc.RouteAdviceRouteStatsRow
	consumers   []sqlc.RouteAdviceConsumersRow
	excessValue int64
	err         error
}

func (s *routeAdviceTestStore) RouteAdviceRouteStats(_ context.Context, arg sqlc.RouteAdviceRouteStatsParams) ([]sqlc.RouteAdviceRouteStatsRow, error) {
	s.stats = arg
	return s.statRows, s.err
}

func (s *routeAdviceTestStore) RouteAdviceConsumers(context.Context, sqlc.RouteAdviceConsumersParams) ([]sqlc.RouteAdviceConsumersRow, error) {
	return s.consumers, nil
}

func (s *routeAdviceTestStore) RouteAdviceThrottleExcess(_ context.Context, arg sqlc.RouteAdviceThrottleExcessParams) (int64, error) {
	s.excess = append(s.excess, arg)
	return s.excessValue, nil
}

func routeAdviceFixture(e testEnv) *routeAdviceTestStore {
	top, next := uuid.New(), uuid.New()
	return &routeAdviceTestStore{
		MemStore: e.store,
		statRows: []sqlc.RouteAdviceRouteStatsRow{
			{Route: "GET /search", Method: "GET", Requests: 10000, AnonymousRequests: 5},
			{Route: "GET /catalog", Method: "GET", Requests: 1000, AnonymousRequests: 990, AnonymousSuccess: 985, ColdBoots: 30, CacheHits: 700, WakesAvoided: 20},
		},
		consumers: []sqlc.RouteAdviceConsumersRow{
			{Route: "GET /search", Method: "GET", ConsumerID: pgtype.UUID{Bytes: top, Valid: true}, Requests: 8000, PeakPerMinute: 900, ConsumerRank: 1, Consumers: 4},
			{Route: "GET /search", Method: "GET", ConsumerID: pgtype.UUID{Bytes: next, Valid: true}, Requests: 1500, PeakPerMinute: 60, ConsumerRank: 2, Consumers: 4},
		},
		excessValue: 4200,
	}
}

func getRouteAdviceResponse(t *testing.T, e testEnv, path string) api.RouteAdviceResponse {
	t.Helper()
	res := e.do(t, "GET", path, nil, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("%d: %s", res.Code, res.Body.String())
	}
	var out api.RouteAdviceResponse
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRouteAdviceSuggestsCacheAndThrottle(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := mustSeedApp(t, e, "shop")
	store := routeAdviceFixture(e)
	e.s.store = store
	out := getRouteAdviceResponse(t, e, "/v1/apps/shop/routes/advice")
	if out.Slug != "shop" || out.CacheMaxAgeSeconds != 60 || out.RoutesAnalyzed != 2 || out.WindowClamped || len(out.Suggestions) != 2 {
		t.Fatalf("response = %+v", out)
	}
	if out.Suggestions[0].Kind != api.RouteAdviceKindThrottle || out.Suggestions[1].Kind != api.RouteAdviceKindCache {
		t.Fatalf("want throttle then cache by volume, got %+v", out.Suggestions)
	}
	if got := out.Suggestions[0].Impact.EstimatedThrottledRequests; got != 4200 {
		t.Fatalf("throttled estimate = %d, want 4200", got)
	}
	host := appHostForDomain("shop", e.s.domain)
	for _, s := range out.Suggestions {
		if len(s.Rules) != 1 || s.Rules[0].MatchHost != host || s.Rules[0].Enabled == nil || *s.Rules[0].Enabled {
			t.Fatalf("rules must be one disabled rule on %s: %+v", host, s.Rules)
		}
	}
	if uuid.UUID(store.stats.AppID.Bytes) != uuid.MustParse(app) || uuid.UUID(store.stats.AccountID.Bytes) != uuid.MustParse(e.acct.ID) ||
		store.stats.RouteLimit != api.RouteAdviceMaxRoutes || store.stats.CacheMaxAgeSeconds != 60 {
		t.Fatalf("stats scope = %+v", store.stats)
	}
	if got := store.stats.UntilAt.Time.Sub(store.stats.SinceAt.Time); got != api.RouteAdviceDefaultSince {
		t.Fatalf("default window = %s, want %s", got, api.RouteAdviceDefaultSince)
	}
	if len(store.excess) != 1 || store.excess[0].Route != "GET /search" || store.excess[0].AllowancePerMinute != 140 {
		t.Fatalf("excess queries = %+v", store.excess)
	}
}

func TestRouteAdviceSkipsRulesTheAppAlreadyHas(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "shop")
	disabled := false
	res := e.do(t, "POST", "/v1/apps/shop/edge-rules", api.CreateEdgeRuleRequest{
		MatchHost: appHostForDomain("shop", e.s.domain), MatchPath: "/catalog", Enabled: &disabled,
		Kind: "cache", Action: json.RawMessage(`{"max_age_seconds":60,"stale_if_error_seconds":300}`),
	}, nil)
	if res.Code != http.StatusCreated {
		t.Fatalf("create rule: %d %s", res.Code, res.Body.String())
	}
	e.s.store = routeAdviceFixture(e)
	out := getRouteAdviceResponse(t, e, "/v1/apps/shop/routes/advice")
	for _, s := range out.Suggestions {
		if s.Kind == api.RouteAdviceKindCache {
			t.Fatalf("an existing (disabled) cache rule must suppress the suggestion: %+v", s)
		}
	}
}

func TestRouteAdviceWhatIfAndWindow(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "shop")
	store := routeAdviceFixture(e)
	e.s.store = store
	out := getRouteAdviceResponse(t, e, "/v1/apps/shop/routes/advice?cache_max_age=300")
	if out.CacheMaxAgeSeconds != 300 || store.stats.CacheMaxAgeSeconds != 300 {
		t.Fatalf("what-if max age not applied: %+v / %+v", out, store.stats)
	}
	if !out.WindowClamped {
		t.Fatal("hobby retention is shorter than the default window; want window_clamped")
	}
}

func TestRouteAdviceRejectsAndDegrades(t *testing.T) {
	free := setup(t, api.PlanFree)
	mustSeedApp(t, free, "shop")
	if res := free.do(t, "GET", "/v1/apps/shop/routes/advice", nil, nil); res.Code != http.StatusPaymentRequired {
		t.Fatalf("free plan: %d %s", res.Code, res.Body.String())
	}
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "shop")
	for _, q := range []string{"cache_max_age=0", "cache_max_age=3601", "cache_max_age=x", "since=nope"} {
		if res := e.do(t, "GET", "/v1/apps/shop/routes/advice?"+q, nil, nil); res.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", q, res.Code, res.Body.String())
		}
	}
	if res := e.do(t, "GET", "/v1/apps/missing/routes/advice", nil, nil); res.Code != http.StatusNotFound {
		t.Fatalf("missing app: %d", res.Code)
	}
	// MemStore has no retained telemetry.
	if res := e.do(t, "GET", "/v1/apps/shop/routes/advice", nil, nil); res.Code != http.StatusServiceUnavailable {
		t.Fatalf("memstore: %d %s", res.Code, res.Body.String())
	}
	store := routeAdviceFixture(e)
	store.err = errors.New("boom")
	e.s.store = store
	if res := e.do(t, "GET", "/v1/apps/shop/routes/advice", nil, nil); res.Code != http.StatusServiceUnavailable {
		t.Fatalf("read failure: %d %s", res.Code, res.Body.String())
	}
}
