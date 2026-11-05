package apidserver_test

// adr: 1187, 1199, 20261022184757

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

type routeAwakeTestStore struct {
	MemStore *state.MemStore
	minutes  []sqlc.RouteAwakeMinutesRow
	routes   []sqlc.RouteAwakeRouteMinutesRow
	sources  []sqlc.RouteAwakeSourceMinutesRow
	work     []sqlc.RouteAwakeWorkMinutesRow
	params   sqlc.RouteAwakeRouteMinutesParams
	workArgs sqlc.RouteAwakeWorkMinutesParams
	err      error
}

func (s *routeAwakeTestStore) RouteAwakeMinutes(context.Context, sqlc.RouteAwakeMinutesParams) ([]sqlc.RouteAwakeMinutesRow, error) {
	return s.minutes, s.err
}

func (s *routeAwakeTestStore) RouteAwakeRouteMinutes(_ context.Context, arg sqlc.RouteAwakeRouteMinutesParams) ([]sqlc.RouteAwakeRouteMinutesRow, error) {
	s.params = arg
	return s.routes, s.err
}

func (s *routeAwakeTestStore) RouteAwakeSourceMinutes(context.Context, sqlc.RouteAwakeSourceMinutesParams) ([]sqlc.RouteAwakeSourceMinutesRow, error) {
	return s.sources, s.err
}

func (s *routeAwakeTestStore) RouteAwakeWorkMinutes(_ context.Context, arg sqlc.RouteAwakeWorkMinutesParams) ([]sqlc.RouteAwakeWorkMinutesRow, error) {
	s.workArgs = arg
	return s.work, s.err
}

// routeAwakeFixture is an app awake for the last two hours, pinged once a
// minute on GET /health. Each minute bills the app's own RAM + 8 MB.
func routeAwakeFixture(t *testing.T, e apidserver.TestEnv, appID string) *routeAwakeTestStore {
	t.Helper()
	app, err := (*e.Store()).AppByID(t.Context(), appID)
	if err != nil {
		t.Fatal(err)
	}
	store := &routeAwakeTestStore{MemStore: *e.Store()}
	start := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Hour)
	for m := 0; m < 120; m++ {
		at := pgtype.Timestamptz{Time: start.Add(time.Duration(m) * time.Minute), Valid: true}
		store.minutes = append(store.minutes, sqlc.RouteAwakeMinutesRow{Minute: at, MbSeconds: int64(60 * (app.RAMMB + api.PerVMOverheadMB))})
		store.routes = append(store.routes, sqlc.RouteAwakeRouteMinutesRow{Minute: at, Route: "GET /health", Method: "GET", Requests: 1, AnonymousRequests: 1})
	}
	return store
}

func TestRouteAwakeReport(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := routeAwakeFixture(t, e, apidserver.MustSeedApp(t, e, "shop"))
	store.sources = []sqlc.RouteAwakeSourceMinutesRow{{Minute: store.minutes[0].Minute, Source: api.RouteAwakeSourcePrewarm, Events: 1}}
	(*(*e.S()).Store()) = statetest.NewMem(store)
	res := e.Do(t, "GET", "/v1/apps/shop/routes/awake", nil, nil)
	var out api.RouteAwakeResponse
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &out) != nil {
		t.Fatalf("%d: %s", res.Code, res.Body.String())
	}
	if out.Slug != "shop" || out.RoutesLimit != api.RouteAwakeMaxRoutes || store.params.RouteLimit != api.RouteAwakeMaxRoutes || !store.params.AppID.Valid {
		t.Fatalf("out = %+v params = %+v", out, store.params)
	}
	if want := api.MustLimitsFor(api.PlanPro).EffectiveIdleTimeoutS(string(state.AppTypeApp), 0); out.IdleTimeoutSeconds != want {
		t.Fatalf("idle timeout = %d, want %d", out.IdleTimeoutSeconds, want)
	}
	if len(out.Routes) != 1 || out.Routes[0].Route != "/health" || out.Routes[0].Pattern != api.RouteAwakePatternPeriodic || out.Routes[0].Suggestion == nil {
		t.Fatalf("routes = %+v", out.Routes)
	}
	if out.AwakeSeconds != 120*60 || out.UnattributedSeconds != 0 {
		t.Fatalf("awake = %d unattributed = %d", out.AwakeSeconds, out.UnattributedSeconds)
	}
	// The prewarm reaches the report; customer requests explain every minute,
	// so it is credited with none of them (ADR-1199).
	if len(out.Sources) != 1 || out.Sources[0].Source != api.RouteAwakeSourcePrewarm || out.Sources[0].Events != 1 ||
		out.Sources[0].AwakeSeconds != 0 || out.MinInstances != 0 {
		t.Fatalf("sources = %+v min_instances = %d", out.Sources, out.MinInstances)
	}
}

func TestRouteAwakeRejectsAndDegrades(t *testing.T) {
	free := apidserver.Setup(t, api.PlanFree)
	apidserver.MustSeedApp(t, free, "shop")
	if res := free.Do(t, "GET", "/v1/apps/shop/routes/awake", nil, nil); res.Code != http.StatusPaymentRequired {
		t.Fatalf("free plan: %d %s", res.Code, res.Body.String())
	}
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "shop")
	if res := e.Do(t, "GET", "/v1/apps/shop/routes/awake?since=nope", nil, nil); res.Code != http.StatusBadRequest {
		t.Fatalf("bad since: %d %s", res.Code, res.Body.String())
	}
	if res := e.Do(t, "GET", "/v1/apps/missing/routes/awake", nil, nil); res.Code != http.StatusNotFound {
		t.Fatalf("missing app: %d", res.Code)
	}
	// MemStore has no retained telemetry.
	if res := e.Do(t, "GET", "/v1/apps/shop/routes/awake", nil, nil); res.Code != http.StatusServiceUnavailable {
		t.Fatalf("memstore: %d %s", res.Code, res.Body.String())
	}
	store := routeAwakeFixture(t, e, appID)
	store.err = errors.New("boom")
	(*(*e.S()).Store()) = statetest.NewMem(store)
	if res := e.Do(t, "GET", "/v1/apps/shop/routes/awake", nil, nil); res.Code != http.StatusServiceUnavailable {
		t.Fatalf("read failure: %d %s", res.Code, res.Body.String())
	}
}

// Requests stop after ten minutes and a cron job fires every minute after
// that: the cron job, named by its path and schedule, explains the rest of
// the awake time, and the report says the plan's waitUntil timeout.
func TestRouteAwakeReportCreditsBackgroundWork(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := routeAwakeFixture(t, e, apidserver.MustSeedApp(t, e, "shop"))
	store.routes = store.routes[:10]
	for m := 10; m < 120; m++ {
		store.work = append(store.work, sqlc.RouteAwakeWorkMinutesRow{
			Minute: store.minutes[m].Minute, Source: api.RouteAwakeSourceCron, Detail: "11111111-1111-4111-8111-111111111111",
			CronSchedule: "* * * * *", CronTarget: "/sync", Running: 1, Started: 1,
		})
	}
	(*(*e.S()).Store()) = statetest.NewMem(store)
	res := e.Do(t, "GET", "/v1/apps/shop/routes/awake", nil, nil)
	var out api.RouteAwakeResponse
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &out) != nil {
		t.Fatalf("%d: %s", res.Code, res.Body.String())
	}
	if store.workArgs.MaxMinutes != api.RouteAwakeWorkMaxMinutes || !store.workArgs.AppID.Valid {
		t.Fatalf("work args = %+v", store.workArgs)
	}
	if len(out.Sources) != 1 || out.Sources[0].Source != api.RouteAwakeSourceCron || out.Sources[0].Name != "/sync (* * * * *)" ||
		out.Sources[0].Events != 110 || out.Sources[0].Suggestion == nil || out.UnattributedSeconds != 0 {
		t.Fatalf("sources = %+v unattributed = %d", out.Sources, out.UnattributedSeconds)
	}
	if want := api.MustLimitsFor(api.PlanPro).WaitUntilTimeoutS(); out.WaitUntilTimeoutSeconds != want {
		t.Fatalf("wait_until_timeout_seconds = %d, want %d", out.WaitUntilTimeoutSeconds, want)
	}
}
