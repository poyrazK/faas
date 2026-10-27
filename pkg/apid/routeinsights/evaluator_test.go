package routeinsights_test

// adr: 1198, 1199

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
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

// fakeStore serves apps that ran in the window: busyApp is pinged every
// minute and has a stable catalog, failApp's reads fail, the rest are quiet.
// It keeps the evaluation claims the way route_insight_evaluations does;
// apps in taken are claimed by another replica first.
type fakeStore struct {
	now       time.Time
	accounts  map[string]state.Account
	apps      map[string]state.App
	busyApp   string
	failApp   string
	taken     map[string]bool
	evaluated map[string]time.Time
	upserts   []sqlc.UpsertRouteInsightParams
	resolves  []sqlc.ResolveStaleRouteInsightsBatchParams
	reads     int
}

func (f *fakeStore) RouteInsightCandidateApps(_ context.Context, arg sqlc.RouteInsightCandidateAppsParams) ([]sqlc.RouteInsightCandidateAppsRow, error) {
	f.reads++
	var due []state.App
	for _, app := range f.apps {
		if !slices.Contains(arg.Plans, string(f.accounts[app.AccountID].Plan)) {
			continue
		}
		if at, ok := f.evaluated[app.ID]; ok && !at.Before(arg.DueBefore.Time) {
			continue
		}
		due = append(due, app)
	}
	slices.SortFunc(due, func(a, b state.App) int {
		if c := f.evaluated[a.ID].Compare(f.evaluated[b.ID]); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	var out []sqlc.RouteInsightCandidateAppsRow
	for _, app := range due[:min(len(due), int(arg.MaxApps))] {
		out = append(out, sqlc.RouteInsightCandidateAppsRow{AppID: pg(app.ID), AccountID: pg(app.AccountID)})
	}
	return out, nil
}

func (f *fakeStore) ClaimRouteInsightEvaluation(_ context.Context, arg sqlc.ClaimRouteInsightEvaluationParams) (int64, error) {
	id := uuid.UUID(arg.AppID.Bytes).String()
	if f.evaluated == nil {
		f.evaluated = map[string]time.Time{}
	}
	if f.taken[id] {
		f.evaluated[id] = arg.EvaluatedAt.Time
		return 0, nil
	}
	if at, ok := f.evaluated[id]; ok && !at.Before(arg.DueBefore.Time) {
		return 0, nil
	}
	f.evaluated[id] = arg.EvaluatedAt.Time
	return 1, nil
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
	switch uuid.UUID(arg.AppID.Bytes).String() {
	case f.failApp:
		return nil, errors.New("boom")
	case f.busyApp:
	default:
		return nil, nil
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

func (f *fakeStore) RouteAwakeWorkMinutes(context.Context, sqlc.RouteAwakeWorkMinutesParams) ([]sqlc.RouteAwakeWorkMinutesRow, error) {
	return nil, nil
}

func (f *fakeStore) RouteAwakeRouteMinutes(_ context.Context, arg sqlc.RouteAwakeRouteMinutesParams) ([]sqlc.RouteAwakeRouteMinutesRow, error) {
	if uuid.UUID(arg.AppID.Bytes).String() != f.busyApp {
		return nil, nil
	}
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

func (f *fakeStore) ResolveStaleRouteInsightsBatch(_ context.Context, arg sqlc.ResolveStaleRouteInsightsBatchParams) (int64, error) {
	f.resolves = append(f.resolves, arg)
	return 3, nil
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
		busyApp: proApp,
		failApp: brokenApp,
	}
	e := &routeinsights.Evaluator{Store: store, Now: func() time.Time { return now }}
	stats, err := e.RunOnce(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// adr: 20261024154324 — the Free app has no request telemetry, so it is
	// not a candidate at all.
	if stats.Apps != 2 || stats.Skipped != 0 || stats.Failed != 1 || stats.Observed != 2 || stats.Resolved != 3 {
		t.Fatalf("stats = %+v", stats)
	}
	if _, ok := store.evaluated[freeApp]; ok {
		t.Fatal("the Free app was claimed")
	}
	kinds := map[string]sqlc.UpsertRouteInsightParams{}
	for _, u := range store.upserts {
		if uuid.UUID(u.AppID.Bytes).String() != proApp || !u.ObservedAt.Time.Equal(now) {
			t.Fatalf("upsert = %+v", u)
		}
		// adr: 20261020161310 — a fixed insight reopens only after a full window.
		if !u.RefixBefore.Time.Equal(now.Add(-api.RouteInsightWindow)) {
			t.Fatalf("refix bound = %v, want %v", u.RefixBefore.Time, now.Add(-api.RouteInsightWindow))
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
	// Stale insights resolve for every app, including ones no longer
	// evaluated, in one bounded batch per run.
	if len(store.resolves) != 1 || !store.resolves[0].StaleBefore.Time.Equal(now.Add(-api.RouteInsightResolveAfter)) ||
		store.resolves[0].RowLimit != api.RouteInsightResolveBatch {
		t.Fatalf("resolves = %+v", store.resolves)
	}
}

// TestEvaluatorEvaluatesEveryAppPastOneBatch is the starvation regression
// (ADR-20261024154324): with more apps than one candidate batch, one run
// evaluates all of them, the next run finds none due, and after half an
// interval every app is due again.
func TestEvaluatorEvaluatesEveryAppPastOneBatch(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	acct := uuid.NewString()
	store := &fakeStore{now: now, accounts: map[string]state.Account{acct: {ID: acct, Plan: api.PlanHobby}}, apps: map[string]state.App{}}
	total := 2*api.RouteInsightBatchApps + 7
	for range total {
		id := uuid.NewString()
		store.apps[id] = state.App{ID: id, AccountID: acct, Slug: "app", Type: state.AppTypeApp, RAMMB: 256}
	}
	clock := now
	e := &routeinsights.Evaluator{Store: store, Now: func() time.Time { return clock }}
	stats, err := e.RunOnce(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Apps != total || store.reads != 3 {
		t.Fatalf("first run evaluated %d of %d apps in %d reads", stats.Apps, total, store.reads)
	}
	clock = now.Add(time.Hour)
	if stats, err = e.RunOnce(t.Context()); err != nil || stats.Apps != 0 {
		t.Fatalf("second run evaluated %d apps, err %v; want none due", stats.Apps, err)
	}
	clock = now.Add(api.RouteInsightInterval)
	if stats, err = e.RunOnce(t.Context()); err != nil || stats.Apps != total {
		t.Fatalf("run after an interval evaluated %d apps, err %v; want %d", stats.Apps, err, total)
	}
}

// TestEvaluatorSkipsAppsAnotherReplicaTook checks that a claim lost to
// another replica is counted and not evaluated twice.
func TestEvaluatorSkipsAppsAnotherReplicaTook(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	acct, mine, theirs := uuid.NewString(), uuid.NewString(), uuid.NewString()
	store := &fakeStore{
		now:      now,
		accounts: map[string]state.Account{acct: {ID: acct, Plan: api.PlanPro}},
		apps: map[string]state.App{
			mine:   {ID: mine, AccountID: acct, Slug: "mine", Type: state.AppTypeApp, RAMMB: 256},
			theirs: {ID: theirs, AccountID: acct, Slug: "theirs", Type: state.AppTypeApp, RAMMB: 256},
		},
		taken: map[string]bool{theirs: true},
	}
	e := &routeinsights.Evaluator{Store: store, Now: func() time.Time { return now }}
	stats, err := e.RunOnce(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Apps != 1 || stats.Taken != 1 {
		t.Fatalf("stats = %+v, want one app evaluated and one taken", stats)
	}
}
