package apidserver_test

// adr: 1198

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type routeInsightsTestStore struct {
	MemStore *state.MemStore
	rows     []sqlc.ListRouteInsightsRow
	params   sqlc.ListRouteInsightsParams
	err      error
}

func (s *routeInsightsTestStore) ListRouteInsights(_ context.Context, arg sqlc.ListRouteInsightsParams) ([]sqlc.ListRouteInsightsRow, error) {
	s.params = arg
	return s.rows, s.err
}

func TestRouteInsightsList(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "shop")
	at := pgtype.Timestamptz{Time: time.Date(2026, 10, 15, 6, 0, 0, 0, time.UTC), Valid: true}
	store := &routeInsightsTestStore{MemStore: *e.Store(), rows: []sqlc.ListRouteInsightsRow{{
		Kind: api.RouteInsightKeptAwake, Method: "GET", Route: "/health", State: api.RouteInsightActive,
		Message: "GET /health keeps shop awake", Evidence: []byte(`{"awake_share":0.96}`),
		FirstDetectedAt: at, LastDetectedAt: at,
	}}}
	(*(*e.S()).Store()) = statetest.NewMem(store)
	res := e.Do(t, "GET", "/v1/apps/shop/routes/insights", nil, nil)
	var out api.RouteInsightsResponse
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &out) != nil {
		t.Fatalf("%d: %s", res.Code, res.Body.String())
	}
	if out.Slug != "shop" || len(out.Insights) != 1 || out.Insights[0].Route != "/health" || string(out.Insights[0].Evidence) != `{"awake_share":0.96}` ||
		out.Insights[0].FirstDetectedAt != "2026-10-15T06:00:00Z" || out.Insights[0].ResolvedAt != "" {
		t.Fatalf("out = %+v", out)
	}
	if store.params.RowLimit != api.RouteInsightListLimit || !store.params.AppID.Valid || !store.params.AccountID.Valid {
		t.Fatalf("params = %+v", store.params)
	}
}

func TestRouteInsightsRejectsAndDegrades(t *testing.T) {
	free := apidserver.Setup(t, api.PlanFree)
	apidserver.MustSeedApp(t, free, "shop")
	if res := free.Do(t, "GET", "/v1/apps/shop/routes/insights", nil, nil); res.Code != http.StatusPaymentRequired {
		t.Fatalf("free plan: %d %s", res.Code, res.Body.String())
	}
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "shop")
	if res := e.Do(t, "GET", "/v1/apps/missing/routes/insights", nil, nil); res.Code != http.StatusNotFound {
		t.Fatalf("missing app: %d", res.Code)
	}
	// MemStore has no retained telemetry and therefore no insights.
	if res := e.Do(t, "GET", "/v1/apps/shop/routes/insights", nil, nil); res.Code != http.StatusServiceUnavailable {
		t.Fatalf("memstore: %d %s", res.Code, res.Body.String())
	}
	(*(*e.S()).Store()) = statetest.NewMem(&routeInsightsTestStore{MemStore: *e.Store(), err: errors.New("boom")})
	if res := e.Do(t, "GET", "/v1/apps/shop/routes/insights", nil, nil); res.Code != http.StatusServiceUnavailable {
		t.Fatalf("read failure: %d %s", res.Code, res.Body.String())
	}
}
