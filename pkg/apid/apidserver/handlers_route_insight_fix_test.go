package apidserver_test

// adr: 20261020161310

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// routeInsightFixStore is the route_insights table for one app; edge rules
// stay in the wrapped MemStore, so the fix runs the real edge-rule core.
type routeInsightFixStore struct {
	MemStore *state.MemStore
	rows     []sqlc.ListRouteInsightsRow
	marked   []sqlc.MarkRouteInsightsFixedParams
}

func (s *routeInsightFixStore) ListRouteInsights(context.Context, sqlc.ListRouteInsightsParams) ([]sqlc.ListRouteInsightsRow, error) {
	return s.rows, nil
}

func (s *routeInsightFixStore) GetRouteInsight(_ context.Context, arg sqlc.GetRouteInsightParams) (sqlc.GetRouteInsightRow, error) {
	for _, r := range s.rows {
		if r.ID == arg.ID {
			return sqlc.GetRouteInsightRow(r), nil
		}
	}
	return sqlc.GetRouteInsightRow{}, state.ErrNotFound
}

func (s *routeInsightFixStore) MarkRouteInsightsFixed(_ context.Context, arg sqlc.MarkRouteInsightsFixedParams) (int64, error) {
	s.marked = append(s.marked, arg)
	for i, r := range s.rows {
		if r.Method == arg.Method && r.Route == arg.Route && slices.Contains(arg.Kinds, r.Kind) {
			s.rows[i].State, s.rows[i].ResolvedAt, s.rows[i].FixedAt, s.rows[i].FixAction = api.RouteInsightResolved, arg.FixedAt, arg.FixedAt, arg.FixAction
		}
	}
	return int64(len(s.rows)), nil
}

func TestRouteInsightFixCreatesTheCacheRule(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "shop")
	at := pgtype.Timestamptz{Time: time.Now().UTC().Add(-time.Hour), Valid: true}
	id := uuid.New()
	store := &routeInsightFixStore{MemStore: *e.Store(), rows: []sqlc.ListRouteInsightsRow{{
		ID: pgtype.UUID{Bytes: id, Valid: true}, Kind: api.RouteInsightCacheable, Method: "GET", Route: "/catalog", State: api.RouteInsightActive,
		Message: "GET /catalog is safe to cache", Evidence: []byte(`{"safe_max_age_seconds":300}`), FirstDetectedAt: at, LastDetectedAt: at,
	}}}
	(*(*e.S()).Store()) = statetest.NewMem(store)

	res := e.Do(t, "GET", "/v1/apps/shop/routes/insights", nil, nil)
	var listed api.RouteInsightsResponse
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &listed) != nil || len(listed.Insights) != 1 || listed.Insights[0].Fix == nil {
		t.Fatalf("list: %d %s", res.Code, res.Body.String())
	}
	fix := listed.Insights[0].Fix
	if listed.Insights[0].ID != id.String() || fix.Action != api.RouteInsightFixCreateCacheRule || fix.MaxAgeSeconds != 300 {
		t.Fatalf("listed = %+v fix = %+v", listed.Insights[0], fix)
	}
	path := "/v1/apps/shop/routes/insights/" + id.String() + "/fix"
	if res := e.Do(t, "POST", path, map[string]any{"fix_id": "stale"}, nil); res.Code != http.StatusConflict {
		t.Fatalf("stale fix id: %d %s", res.Code, res.Body.String())
	}
	if res := e.Do(t, "POST", path, map[string]any{}, nil); res.Code != http.StatusBadRequest {
		t.Fatalf("missing fix id: %d %s", res.Code, res.Body.String())
	}
	res = e.Do(t, "POST", path, map[string]any{"fix_id": fix.ID}, nil)
	var out api.RouteInsightFixResponse
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &out) != nil {
		t.Fatalf("fix: %d %s", res.Code, res.Body.String())
	}
	if out.Rule.ID == "" || out.Rule.Kind != "cache" || out.Rule.MatchHost != api.EdgeRuleAppHosts || out.Insight.FixAction != api.RouteInsightFixCreateCacheRule {
		t.Fatalf("out = %+v", out)
	}
	rules, err := store.MemStore.ListEdgeRulesForApp(t.Context(), appID)
	if err != nil || len(rules) != 1 || !rules[0].Enabled || rules[0].MatchPath != "/catalog" || rules[0].Action.Cache == nil || rules[0].Action.Cache.MaxAgeSeconds != 300 {
		t.Fatalf("rules = %+v err = %v", rules, err)
	}
	if len(store.marked) != 1 || !slices.Contains(store.marked[0].Kinds, api.RouteInsightKeptAwake) {
		t.Fatalf("marked = %+v", store.marked)
	}
	// The insight is resolved now, so a replayed fix is refused.
	if res := e.Do(t, "POST", path, map[string]any{"fix_id": fix.ID}, nil); res.Code != http.StatusConflict {
		t.Fatalf("second fix: %d %s", res.Code, res.Body.String())
	}
}

func TestRouteInsightFixRejects(t *testing.T) {
	free := apidserver.Setup(t, api.PlanFree)
	apidserver.MustSeedApp(t, free, "shop")
	path := "/v1/apps/shop/routes/insights/" + uuid.NewString() + "/fix"
	if res := free.Do(t, "POST", path, map[string]any{"fix_id": "x"}, nil); res.Code != http.StatusPaymentRequired {
		t.Fatalf("free plan: %d %s", res.Code, res.Body.String())
	}
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "shop")
	if res := e.Do(t, "POST", path, map[string]any{"fix_id": "x"}, nil); res.Code != http.StatusNotFound {
		t.Fatalf("unknown insight: %d %s", res.Code, res.Body.String())
	}
	if res := e.Do(t, "POST", "/v1/apps/missing/routes/insights/"+uuid.NewString()+"/fix", map[string]any{"fix_id": "x"}, nil); res.Code != http.StatusNotFound {
		t.Fatalf("missing app: %d", res.Code)
	}
}
