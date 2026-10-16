package routeinsights_test

// adr: 1198, 1199

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/routeinsights"
	"github.com/onebox-faas/faas/pkg/routeadvisor"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func stableCatalog() routeadvisor.RouteStats {
	return routeadvisor.RouteStats{
		Method: "GET", Path: "/catalog", Requests: 1000, AnonymousRequests: 990, AnonymousSuccess: 980,
		ColdBoots: 40, CacheHits: 600, WakesAvoided: 25,
		Content: routeadvisor.ContentStats{First: 30, Same: []int64{300, 200, 100}, Changed: []int64{0, 1, 2}},
	}
}

func TestKeptAwake(t *testing.T) {
	report := api.RouteAwakeResponse{AwakeShare: 0.9, WindowSeconds: 86400, IdleTimeoutSeconds: 60, Routes: []api.RouteAwakeRoute{
		{Method: "GET", Route: "/health", IntervalSeconds: 60, IfRemoved: api.RouteAwakeWhatIf{AwakeSecondsSaved: 70000, WakesChange: 30},
			Suggestion: &api.RouteAwakeSuggestion{Kind: api.RouteAwakeSuggestEdgeCache, Message: "GET /health keeps shop awake"}},
		{Method: "POST", Route: "/orders"},
	}}
	got := routeinsights.KeptAwake(report)
	if len(got) != 1 || got[0].Kind != routeinsights.KindKeptAwake || got[0].Route != "/health" || got[0].Message != "GET /health keeps shop awake" {
		t.Fatalf("findings = %+v", got)
	}
	if got[0].Evidence["awake_seconds_saved"] != int64(70000) || got[0].Evidence["suggestion"] != api.RouteAwakeSuggestEdgeCache {
		t.Fatalf("evidence = %+v", got[0].Evidence)
	}
	report.AwakeShare = 0.3
	if got := routeinsights.KeptAwake(report); len(got) != 0 {
		t.Fatalf("an app that parks most of the day is not kept awake: %+v", got)
	}
}

func TestCacheable(t *testing.T) {
	got := routeinsights.Cacheable("shop", []routeadvisor.RouteStats{stableCatalog()}, nil)
	if len(got) != 1 || got[0].Kind != routeinsights.KindCacheable || got[0].Route != "/catalog" || got[0].Evidence["safe_max_age_seconds"] != 3600 {
		t.Fatalf("findings = %+v", got)
	}
	if !strings.Contains(got[0].Message, "--cache-max-age 3600") {
		t.Fatalf("message = %q", got[0].Message)
	}
	unmeasured := stableCatalog()
	unmeasured.Content = routeadvisor.ContentStats{}
	if got := routeinsights.Cacheable("shop", []routeadvisor.RouteStats{unmeasured}, nil); len(got) != 0 {
		t.Fatalf("an unmeasured route is not an insight: %+v", got)
	}
	covered := []routeadvisor.ExistingRule{{Kind: "cache", Path: "/catalog"}}
	if got := routeinsights.Cacheable("shop", []routeadvisor.RouteStats{stableCatalog()}, covered); len(got) != 0 {
		t.Fatalf("a route with a cache rule is not an insight: %+v", got)
	}
}

// TestCacheRules checks existing cache rules against their refills
// (ADR-1199).
func TestCacheRules(t *testing.T) {
	rules := []routeadvisor.ExistingRule{
		{Kind: "cache", Path: "/prices", Enabled: true, MaxAgeSeconds: 3600},
		{Kind: "cache", Path: "/catalog", Enabled: true, MaxAgeSeconds: 60},
		{Kind: "cache", Path: "/off", Enabled: false, MaxAgeSeconds: 60},
	}
	routes := []routeadvisor.RouteStats{
		{Method: "GET", Path: "/prices", Requests: 900, Content: routeadvisor.ContentStats{Same: []int64{0, 0, 30}, Changed: []int64{0, 0, 0}, Stale: 20}},
		{Method: "GET", Path: "/catalog", Requests: 5000, Content: routeadvisor.ContentStats{Same: []int64{0, 30, 40}, Changed: []int64{0, 0, 0}}},
		{Method: "GET", Path: "/off", Requests: 5000, Content: routeadvisor.ContentStats{Same: []int64{0, 30, 40}, Changed: []int64{0, 0, 0}}},
		{Method: "GET", Path: "/uncached", Requests: 5000, Content: routeadvisor.ContentStats{Stale: 40}},
	}
	got := routeinsights.CacheRules(routes, rules)
	if len(got) != 2 {
		t.Fatalf("findings = %+v", got)
	}
	stale, short := got[0], got[1]
	if stale.Kind != routeinsights.KindCacheStale || stale.Route != "/prices" || stale.Evidence["stale_refills"] != int64(20) ||
		!strings.Contains(stale.Message, "20 of 50 refills (40%)") {
		t.Fatalf("stale = %+v", stale)
	}
	if short.Kind != routeinsights.KindCacheTooShort || short.Route != "/catalog" || short.Evidence["safe_max_age_seconds"] != 3600 ||
		!strings.Contains(short.Message, "A max-age of 3600s") {
		t.Fatalf("too short = %+v", short)
	}
}

// fakeStore serves one Pro app that is pinged every minute and has a stable
// catalog, one Free app, and one app whose reads fail.
type fakeStore struct {
	now      time.Time
	accounts map[string]state.Account
	apps     map[string]state.App
	failApp  string
	upserts  []sqlc.UpsertRouteInsightParams
	resolved []string
}

func (f *fakeStore) RouteInsightCandidateApps(context.Context, sqlc.RouteInsightCandidateAppsParams) ([]sqlc.RouteInsightCandidateAppsRow, error) {
	var out []sqlc.RouteInsightCandidateAppsRow
	for _, app := range f.apps {
		out = append(out, sqlc.RouteInsightCandidateAppsRow{AppID: pg(app.ID), AccountID: pg(app.AccountID)})
	}
	return out, nil
}

func (f *fakeStore) AccountByID(_ context.Context, id string) (state.Account, error) {
	return f.accounts[id], nil
}

func (f *fakeStore) AppByID(_ context.Context, id string) (state.App, error) {
	return f.apps[id], nil
}

func (f *fakeStore) ListEdgeRulesForApp(context.Context, string) ([]state.EdgeRule, error) {
	return nil, nil
}

func (f *fakeStore) RouteAwakeMinutes(_ context.Context, arg sqlc.RouteAwakeMinutesParams) ([]sqlc.RouteAwakeMinutesRow, error) {
	if uuid.UUID(arg.AppID.Bytes).String() == f.failApp {
		return nil, errors.New("boom")
	}
	var out []sqlc.RouteAwakeMinutesRow
	for m := 0; m < 24*60; m++ {
		out = append(out, sqlc.RouteAwakeMinutesRow{Minute: ts(f.now.Add(-24 * time.Hour).Add(time.Duration(m) * time.Minute)), MbSeconds: 60 * (256 + api.PerVMOverheadMB)})
	}
	return out, nil
}

func (f *fakeStore) RouteAwakeSourceMinutes(context.Context, sqlc.RouteAwakeSourceMinutesParams) ([]sqlc.RouteAwakeSourceMinutesRow, error) {
	return nil, nil
}

func (f *fakeStore) RouteAwakeRouteMinutes(context.Context, sqlc.RouteAwakeRouteMinutesParams) ([]sqlc.RouteAwakeRouteMinutesRow, error) {
	var out []sqlc.RouteAwakeRouteMinutesRow
	for m := 0; m < 24*60; m++ {
		out = append(out, sqlc.RouteAwakeRouteMinutesRow{Minute: ts(f.now.Add(-24 * time.Hour).Add(time.Duration(m) * time.Minute)), Route: "GET /health", Method: "GET", Requests: 1, AnonymousRequests: 1})
	}
	return out, nil
}

func (f *fakeStore) RouteAdviceRouteStats(context.Context, sqlc.RouteAdviceRouteStatsParams) ([]sqlc.RouteAdviceRouteStatsRow, error) {
	c := stableCatalog()
	return []sqlc.RouteAdviceRouteStatsRow{{
		Route: "GET /catalog", Method: "GET", Requests: c.Requests, AnonymousRequests: c.AnonymousRequests, AnonymousSuccess: c.AnonymousSuccess,
		ColdBoots: c.ColdBoots, CacheHits: c.CacheHits, WakesAvoided: c.WakesAvoided,
		ContentFirst: 30, ContentSame60: 300, ContentSame300: 200, ContentSame3600: 100, ContentChanged300: 1, ContentChanged3600: 2,
	}}, nil
}

func (f *fakeStore) UpsertRouteInsight(_ context.Context, arg sqlc.UpsertRouteInsightParams) error {
	f.upserts = append(f.upserts, arg)
	return nil
}

func (f *fakeStore) ResolveStaleRouteInsights(_ context.Context, arg sqlc.ResolveStaleRouteInsightsParams) (int64, error) {
	if !arg.StaleBefore.Time.Equal(f.now.Add(-api.RouteInsightResolveAfter)) {
		return 0, errors.New("unexpected stale bound")
	}
	f.resolved = append(f.resolved, uuid.UUID(arg.AppID.Bytes).String())
	return 1, nil
}

func pg(id string) pgtype.UUID { return pgtype.UUID{Bytes: uuid.MustParse(id), Valid: true} }

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func TestEvaluatorRunOnce(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	pro, free, broken := uuid.NewString(), uuid.NewString(), uuid.NewString()
	proApp, freeApp, brokenApp := uuid.NewString(), uuid.NewString(), uuid.NewString()
	store := &fakeStore{
		now:      now,
		accounts: map[string]state.Account{pro: {ID: pro, Plan: api.PlanPro}, free: {ID: free, Plan: api.PlanFree}, broken: {ID: broken, Plan: api.PlanPro}},
		apps: map[string]state.App{
			proApp:    {ID: proApp, AccountID: pro, Slug: "shop", Type: state.AppTypeApp, RAMMB: 256},
			freeApp:   {ID: freeApp, AccountID: free, Slug: "hobby", Type: state.AppTypeApp, RAMMB: 128},
			brokenApp: {ID: brokenApp, AccountID: broken, Slug: "broken", Type: state.AppTypeApp, RAMMB: 256},
		},
		failApp: brokenApp,
	}
	e := &routeinsights.Evaluator{Store: store, Now: func() time.Time { return now }}
	stats, err := e.RunOnce(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Apps != 3 || stats.Skipped != 1 || stats.Failed != 1 || stats.Observed != 2 {
		t.Fatalf("stats = %+v", stats)
	}
	kinds := map[string]sqlc.UpsertRouteInsightParams{}
	for _, u := range store.upserts {
		if uuid.UUID(u.AppID.Bytes).String() != proApp || !u.ObservedAt.Time.Equal(now) {
			t.Fatalf("upsert = %+v", u)
		}
		var evidence map[string]any
		if err := json.Unmarshal(u.Evidence, &evidence); err != nil {
			t.Fatalf("evidence is not JSON: %v", err)
		}
		kinds[u.Kind] = u
	}
	if kinds[routeinsights.KindKeptAwake].Route != "/health" || kinds[routeinsights.KindCacheable].Route != "/catalog" {
		t.Fatalf("upserts = %+v", store.upserts)
	}
	// The Free app has no telemetry but still resolves what went stale.
	if len(store.resolved) != 2 || !contains(store.resolved, freeApp) || !contains(store.resolved, proApp) {
		t.Fatalf("resolved = %v", store.resolved)
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
