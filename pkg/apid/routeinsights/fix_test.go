package routeinsights_test

// adr: 20261020161310

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/routeinsights"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var fixNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func insight(kind, route string, evidence map[string]any) api.RouteInsight {
	raw, _ := json.Marshal(evidence)
	return api.RouteInsight{ID: uuid.NewString(), Kind: kind, Method: "GET", Route: route, State: api.RouteInsightActive, Evidence: raw}
}

func cacheRule(id, path string, maxAge, priority int) state.EdgeRule {
	return state.EdgeRule{
		ID: id, MatchHost: api.EdgeRuleAppHosts, MatchPath: path, Priority: priority, Enabled: true,
		Kind: state.EdgeRuleKindCache, UpdatedAt: fixNow.Add(-time.Hour),
		Action: state.EdgeRuleAction{Cache: &state.EdgeRuleCacheAction{MaxAgeSeconds: maxAge, StaleIfErrorSeconds: 120, VaryOn: []string{"Accept-Language"}}},
	}
}

func cacheAction(t *testing.T, raw []byte) api.EdgeRuleCacheAction {
	t.Helper()
	var a api.EdgeRuleCacheAction
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatalf("action %s: %v", raw, err)
	}
	return a
}

func TestPlanFixCreatesCacheRuleForCacheableRoute(t *testing.T) {
	in := insight(api.RouteInsightCacheable, "/catalog", map[string]any{"safe_max_age_seconds": 300})
	plan, reason := routeinsights.PlanFix(in, []api.RouteInsight{in}, nil, fixNow)
	if plan == nil {
		t.Fatalf("no plan: %s", reason)
	}
	c := plan.Create
	if plan.Fix.Action != api.RouteInsightFixCreateCacheRule || plan.Fix.MaxAgeSeconds != 300 || c == nil || plan.Update != nil {
		t.Fatalf("plan = %+v", plan)
	}
	if c.MatchHost != api.EdgeRuleAppHosts || c.MatchPath != "/catalog" || !slices.Equal(c.MatchMethods, []string{"GET", "HEAD"}) ||
		c.Kind != "cache" || c.Enabled == nil || !*c.Enabled || cacheAction(t, c.Action).MaxAgeSeconds != 300 {
		t.Fatalf("create = %+v", c)
	}
	if !slices.Contains(plan.Kinds, api.RouteInsightKeptAwake) || plan.PurgeGlob != "" || plan.Fix.ID == "" {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestPlanFixKeptAwakeUsesTheRoutesCacheableEvidence(t *testing.T) {
	awake := insight(api.RouteInsightKeptAwake, "/catalog", map[string]any{"awake_share": 0.9})
	if plan, reason := routeinsights.PlanFix(awake, []api.RouteInsight{awake}, nil, fixNow); plan != nil || !strings.Contains(reason, "caller") {
		t.Fatalf("kept_awake alone: plan=%+v reason=%q", plan, reason)
	}
	cacheable := insight(api.RouteInsightCacheable, "/catalog", map[string]any{"safe_max_age_seconds": 600})
	plan, _ := routeinsights.PlanFix(awake, []api.RouteInsight{awake, cacheable}, nil, fixNow)
	if plan == nil || plan.Fix.MaxAgeSeconds != 600 || plan.Create == nil {
		t.Fatalf("plan = %+v", plan)
	}
	other, _ := routeinsights.PlanFix(cacheable, []api.RouteInsight{awake, cacheable}, nil, fixNow)
	if other == nil || other.Fix.ID == plan.Fix.ID {
		t.Fatal("each insight needs its own fix id")
	}
}

func TestPlanFixRuleChanges(t *testing.T) {
	logMode := cacheRule("log", "/catalog", 10, 1)
	logMode.Mode = state.EdgeRuleModeLog
	expired := cacheRule("old", "/catalog", 10, 1)
	past := fixNow.Add(-time.Minute)
	expired.ExpiresAt = &past
	manifest := cacheRule("manifest", "/catalog", 60, 50)
	manifest.ManifestKey = "web"
	tests := []struct {
		name     string
		kind     string
		evidence map[string]any
		rules    []state.EdgeRule
		action   string
		maxAge   int
		purge    bool
		override bool
		reason   string
	}{
		{name: "too short, exact rule", kind: api.RouteInsightCacheTooShort, evidence: map[string]any{"safe_max_age_seconds": 900},
			rules: []state.EdgeRule{cacheRule("r1", "/catalog", 60, 100)}, action: api.RouteInsightFixSetCacheMaxAge, maxAge: 900},
		{name: "too short, broad rule", kind: api.RouteInsightCacheTooShort, evidence: map[string]any{"safe_max_age_seconds": 900},
			rules: []state.EdgeRule{cacheRule("r1", "/*", 60, 100)}, action: api.RouteInsightFixAddCacheOverride, maxAge: 900, override: true},
		{name: "too short, broad rule at priority 0", kind: api.RouteInsightCacheTooShort, evidence: map[string]any{"safe_max_age_seconds": 900},
			rules: []state.EdgeRule{cacheRule("r1", "/*", 60, 0)}, reason: "priority 0"},
		{name: "too short, no longer", kind: api.RouteInsightCacheTooShort, evidence: map[string]any{"safe_max_age_seconds": 60},
			rules: []state.EdgeRule{cacheRule("r1", "/catalog", 60, 100)}, reason: "no longer above"},
		{name: "stale, safe max-age measured", kind: api.RouteInsightCacheStale, evidence: map[string]any{"safe_max_age_seconds": 30},
			rules: []state.EdgeRule{cacheRule("r1", "/catalog", 300, 100)}, action: api.RouteInsightFixSetCacheMaxAge, maxAge: 30, purge: true},
		{name: "stale, nothing safe, exact rule", kind: api.RouteInsightCacheStale, evidence: map[string]any{"safe_max_age_seconds": 0},
			rules: []state.EdgeRule{cacheRule("r1", "/catalog", 300, 100)}, action: api.RouteInsightFixDisableCacheRule, purge: true},
		{name: "stale, nothing safe, broad rule", kind: api.RouteInsightCacheStale, evidence: map[string]any{},
			rules: []state.EdgeRule{cacheRule("r1", "/*", 300, 100)}, reason: "edge-rules page"},
		{name: "manifest rule gets an override", kind: api.RouteInsightCacheTooShort, evidence: map[string]any{"safe_max_age_seconds": 900},
			rules: []state.EdgeRule{manifest}, action: api.RouteInsightFixAddCacheOverride, maxAge: 900, override: true},
		{name: "log-mode and expired rules do not serve", kind: api.RouteInsightCacheTooShort, evidence: map[string]any{"safe_max_age_seconds": 900},
			rules: []state.EdgeRule{logMode, expired}, reason: "no enabled cache rule"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := insight(tc.kind, "/catalog", tc.evidence)
			plan, reason := routeinsights.PlanFix(in, []api.RouteInsight{in}, tc.rules, fixNow)
			if tc.reason != "" {
				if plan != nil || !strings.Contains(reason, tc.reason) {
					t.Fatalf("plan=%+v reason=%q, want reason containing %q", plan, reason, tc.reason)
				}
				return
			}
			if plan == nil {
				t.Fatalf("no plan: %s", reason)
			}
			if plan.Fix.Action != tc.action || plan.Fix.MaxAgeSeconds != tc.maxAge || plan.Fix.PurgeCache != tc.purge || (plan.PurgeGlob != "") != tc.purge {
				t.Fatalf("fix = %+v purge %q", plan.Fix, plan.PurgeGlob)
			}
			rule := tc.rules[0]
			if plan.Fix.RuleID != rule.ID {
				t.Fatalf("rule id = %q, want %q", plan.Fix.RuleID, rule.ID)
			}
			switch {
			case tc.override:
				c := plan.Create
				if c == nil || c.Priority == nil || *c.Priority != rule.Priority-1 || c.MatchHost != rule.MatchHost || c.MatchPath != "/catalog" {
					t.Fatalf("override = %+v", c)
				}
				a := cacheAction(t, c.Action)
				if a.MaxAgeSeconds != tc.maxAge || a.StaleIfErrorSeconds != 120 || !slices.Equal(a.VaryOn, []string{"Accept-Language"}) {
					t.Fatalf("override action = %+v; it must keep the rule's other settings", a)
				}
			case tc.action == api.RouteInsightFixDisableCacheRule:
				if plan.Update == nil || plan.Update.Enabled == nil || *plan.Update.Enabled || plan.Update.Action != nil {
					t.Fatalf("update = %+v", plan.Update)
				}
			default:
				if plan.Update == nil || plan.Update.Action == nil {
					t.Fatalf("update = %+v", plan.Update)
				}
				a := cacheAction(t, *plan.Update.Action)
				if a.MaxAgeSeconds != tc.maxAge || a.StaleIfErrorSeconds != 120 || !slices.Equal(a.VaryOn, []string{"Accept-Language"}) {
					t.Fatalf("update action = %+v; it must keep the rule's other settings", a)
				}
			}
		})
	}
}

func TestPlanFixIDChangesWithTheRule(t *testing.T) {
	in := insight(api.RouteInsightCacheTooShort, "/catalog", map[string]any{"safe_max_age_seconds": 900})
	rule := cacheRule("r1", "/catalog", 60, 100)
	first, _ := routeinsights.PlanFix(in, nil, []state.EdgeRule{rule}, fixNow)
	rule.UpdatedAt = fixNow
	second, _ := routeinsights.PlanFix(in, nil, []state.EdgeRule{rule}, fixNow)
	if first == nil || second == nil || first.Fix.ID == second.Fix.ID {
		t.Fatal("a rule edit must change the fix id")
	}
	in.State = api.RouteInsightResolved
	if plan, reason := routeinsights.PlanFix(in, nil, []state.EdgeRule{rule}, fixNow); plan != nil || reason != "the insight is resolved" {
		t.Fatalf("resolved insight: plan=%+v reason=%q", plan, reason)
	}
}

// fixStore is the route_insights table for one app plus its edge rules.
type fixStore struct {
	rows   []sqlc.ListRouteInsightsRow
	rules  []state.EdgeRule
	marked []sqlc.MarkRouteInsightsFixedParams
}

func (f *fixStore) ListRouteInsights(context.Context, sqlc.ListRouteInsightsParams) ([]sqlc.ListRouteInsightsRow, error) {
	return f.rows, nil
}

func (f *fixStore) ListEdgeRulesForApp(context.Context, string) ([]state.EdgeRule, error) {
	return f.rules, nil
}

func (f *fixStore) GetRouteInsight(_ context.Context, arg sqlc.GetRouteInsightParams) (sqlc.GetRouteInsightRow, error) {
	for _, r := range f.rows {
		if r.ID == arg.ID {
			return sqlc.GetRouteInsightRow(r), nil
		}
	}
	return sqlc.GetRouteInsightRow{}, state.ErrNotFound
}

func (f *fixStore) MarkRouteInsightsFixed(_ context.Context, arg sqlc.MarkRouteInsightsFixedParams) (int64, error) {
	f.marked = append(f.marked, arg)
	var n int64
	for i, r := range f.rows {
		if r.State == api.RouteInsightActive && r.Method == arg.Method && r.Route == arg.Route && slices.Contains(arg.Kinds, r.Kind) {
			f.rows[i].State, f.rows[i].ResolvedAt, f.rows[i].FixedAt, f.rows[i].FixAction = api.RouteInsightResolved, arg.FixedAt, arg.FixedAt, arg.FixAction
			n++
		}
	}
	return n, nil
}

type fakeRules struct {
	created []api.CreateEdgeRuleRequest
	updated map[string]api.UpdateEdgeRuleRequest
	prob    *api.Problem
}

func (r *fakeRules) CreateRule(_ context.Context, _ state.Account, _ state.App, req api.CreateEdgeRuleRequest) (api.EdgeRuleResponse, *api.Problem) {
	if r.prob != nil {
		return api.EdgeRuleResponse{}, r.prob
	}
	r.created = append(r.created, req)
	return api.EdgeRuleResponse{ID: "new-rule", Kind: req.Kind, MatchHost: req.MatchHost}, nil
}

func (r *fakeRules) UpdateRule(_ context.Context, _ state.Account, id string, req api.UpdateEdgeRuleRequest) (api.EdgeRuleResponse, *api.Problem) {
	if r.prob != nil {
		return api.EdgeRuleResponse{}, r.prob
	}
	if r.updated == nil {
		r.updated = map[string]api.UpdateEdgeRuleRequest{}
	}
	r.updated[id] = req
	return api.EdgeRuleResponse{ID: id, Kind: "cache"}, nil
}

func row(kind, route string, evidence map[string]any) sqlc.ListRouteInsightsRow {
	raw, _ := json.Marshal(evidence)
	return sqlc.ListRouteInsightsRow{
		ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Kind: kind, Method: "GET", Route: route, State: api.RouteInsightActive,
		Message: "m", Evidence: raw, FirstDetectedAt: ts(fixNow.Add(-time.Hour)), LastDetectedAt: ts(fixNow),
	}
}

func TestFixerApplies(t *testing.T) {
	acct := state.Account{ID: uuid.NewString(), Plan: api.PlanPro}
	app := state.App{ID: uuid.NewString(), AccountID: acct.ID, Slug: "demo"}

	t.Run("create resolves the route's cacheable and kept_awake insights", func(t *testing.T) {
		store := &fixStore{rows: []sqlc.ListRouteInsightsRow{
			row(api.RouteInsightCacheable, "/catalog", map[string]any{"safe_max_age_seconds": 300}),
			row(api.RouteInsightKeptAwake, "/catalog", map[string]any{}),
			row(api.RouteInsightCacheable, "/other", map[string]any{"safe_max_age_seconds": 300}),
		}}
		rules := &fakeRules{}
		fixer := &routeinsights.Fixer{Store: store, Rules: rules, Now: func() time.Time { return fixNow }}
		listed, err := routeinsights.List(context.Background(), store, acct.ID, app.ID, fixNow)
		if err != nil || listed[0].Fix == nil {
			t.Fatalf("listed = %+v, %v", listed, err)
		}
		out, prob := fixer.Apply(context.Background(), acct, app, listed[0].ID, listed[0].Fix.ID)
		if prob != nil {
			t.Fatalf("apply: %+v", prob)
		}
		if len(rules.created) != 1 || out.Rule.ID != "new-rule" || out.Resolved != 2 || out.CachePurge != "" {
			t.Fatalf("out = %+v created = %+v", out, rules.created)
		}
		if out.Insight.State != api.RouteInsightResolved || out.Insight.FixAction != api.RouteInsightFixCreateCacheRule || out.Insight.FixedAt == "" {
			t.Fatalf("insight after fix = %+v", out.Insight)
		}
		if store.rows[2].State != api.RouteInsightActive {
			t.Fatal("another route's insight was resolved")
		}
	})

	t.Run("refusals write nothing", func(t *testing.T) {
		store := &fixStore{rows: []sqlc.ListRouteInsightsRow{
			row(api.RouteInsightCacheable, "/catalog", map[string]any{"safe_max_age_seconds": 300}),
			row(api.RouteInsightKeptAwake, "/health", map[string]any{}),
		}}
		rules := &fakeRules{}
		fixer := &routeinsights.Fixer{Store: store, Rules: rules, Now: func() time.Time { return fixNow }}
		id := uuid.UUID(store.rows[0].ID.Bytes).String()
		cases := []struct {
			insight, fixID string
			status         int
			code           string
		}{
			{id, "stale", http.StatusConflict, api.CodeRouteInsightFixChanged},
			{uuid.UUID(store.rows[1].ID.Bytes).String(), "any", http.StatusConflict, api.CodeRouteInsightNoFix},
			{uuid.NewString(), "any", http.StatusNotFound, api.CodeNotFound},
			{"not-a-uuid", "any", http.StatusNotFound, api.CodeNotFound},
		}
		for _, c := range cases {
			_, prob := fixer.Apply(context.Background(), acct, app, c.insight, c.fixID)
			if prob == nil || prob.Status != c.status || prob.Code != c.code {
				t.Fatalf("%+v: problem = %+v", c, prob)
			}
		}
		if len(rules.created) != 0 || len(store.marked) != 0 {
			t.Fatalf("refused fixes wrote: created %d, marked %d", len(rules.created), len(store.marked))
		}
	})

	t.Run("an edge-rule refusal is returned and nothing resolves", func(t *testing.T) {
		store := &fixStore{rows: []sqlc.ListRouteInsightsRow{row(api.RouteInsightCacheable, "/catalog", map[string]any{"safe_max_age_seconds": 300})}}
		quota := api.ErrPlanLimitEdgeRules(api.PlanPro, 10, 10)
		fixer := &routeinsights.Fixer{Store: store, Rules: &fakeRules{prob: quota}, Now: func() time.Time { return fixNow }}
		listed, _ := routeinsights.List(context.Background(), store, acct.ID, app.ID, fixNow)
		if _, prob := fixer.Apply(context.Background(), acct, app, listed[0].ID, listed[0].Fix.ID); prob != quota || len(store.marked) != 0 {
			t.Fatalf("problem = %+v, marked = %d", prob, len(store.marked))
		}
	})

	t.Run("a stale fix purges, and a failed purge is reported", func(t *testing.T) {
		for _, purgeErr := range []error{nil, errors.New("notify down")} {
			store := &fixStore{
				rows:  []sqlc.ListRouteInsightsRow{row(api.RouteInsightCacheStale, "/catalog", map[string]any{"safe_max_age_seconds": 30})},
				rules: []state.EdgeRule{cacheRule("r1", "/catalog", 300, 100)},
			}
			var purged []string
			fixer := &routeinsights.Fixer{Store: store, Rules: &fakeRules{}, Now: func() time.Time { return fixNow },
				Purge: func(_ context.Context, _ state.App, glob string) error {
					purged = append(purged, glob)
					return purgeErr
				}}
			listed, _ := routeinsights.List(context.Background(), store, acct.ID, app.ID, fixNow)
			out, prob := fixer.Apply(context.Background(), acct, app, listed[0].ID, listed[0].Fix.ID)
			want := api.RouteInsightPurgeRequested
			if purgeErr != nil {
				want = api.RouteInsightPurgeFailed
			}
			if prob != nil || !slices.Equal(purged, []string{"/catalog"}) || out.CachePurge != want || out.Resolved != 1 {
				t.Fatalf("purge err %v: out = %+v prob = %+v purged = %v", purgeErr, out, prob, purged)
			}
		}
	})
}
