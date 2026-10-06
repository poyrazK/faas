package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type routeCustomerTestStore struct {
	*state.MemStore
	query sqlc.RequestTelemetryRouteCustomersParams
	rows  []sqlc.RequestTelemetryRouteCustomersRow
	err   error
	calls int
}

func (s *routeCustomerTestStore) RequestTelemetryRouteCustomers(_ context.Context, query sqlc.RequestTelemetryRouteCustomersParams) ([]sqlc.RequestTelemetryRouteCustomersRow, error) {
	s.query, s.calls = query, s.calls+1
	return s.rows, s.err
}

func TestRouteCustomerUsageBindingAndBounds(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := mustSeedApp(t, e, "customer-impact")
	d, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app, Kind: state.DeploymentKindImage, ImageDigest: "sha256:test"})
	if err != nil {
		t.Fatal(err)
	}
	at := pgtype.Timestamptz{Time: time.Now().UTC().Add(-time.Hour), Valid: true}
	store := &routeCustomerTestStore{MemStore: e.store, rows: []sqlc.RequestTelemetryRouteCustomersRow{{
		Route: "GET /orders/{id}", Method: "GET", Requests: 50, IdentifiedRequests: 30, AnonymousRequests: 15, UnresolvedIdentityRequests: 5,
		ConsumerCount: 23, PlatformTenantCount: 2, LastObservedAt: at, MatchedRoutes: 201,
		CustomerGroups: 23, OtherCustomerRequests: 10, ConsumerID: uuid.NewString(), CustomerRequests: 20, CustomerLastObservedAt: at,
	}}}
	e.s.store = store
	res := e.do(t, "GET", "/v1/apps/customer-impact/analytics/route-customers?deployment_id="+d.ID+"&since=720h", nil, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("%d: %s", res.Code, res.Body.String())
	}
	var out api.RouteCustomerUsageResponse
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.DeploymentID != d.ID || out.Coverage != "observed_only" || !out.WindowClamped || !out.RoutesTruncated || len(out.Routes) != 1 {
		t.Fatalf("response = %+v", out)
	}
	row := out.Routes[0]
	if row.ConsumerCount != 23 || len(row.Customers) != 1 || !row.CustomersTruncated || row.OtherCustomerRequests != 10 || row.AnonymousRequests != 15 || row.UnresolvedIdentityRequests != 5 {
		t.Fatalf("bounded exposure = %+v", row)
	}
	if uuid.UUID(store.query.AppID.Bytes) != uuid.MustParse(app) || uuid.UUID(store.query.AccountID.Bytes) != uuid.MustParse(e.acct.ID) || uuid.UUID(store.query.DeploymentID.Bytes) != uuid.MustParse(d.ID) || store.query.RouteLimit != 200 || store.query.CustomerLimit != 20 {
		t.Fatalf("scope = %+v", store.query)
	}
}

func TestRouteCustomerUsageRejectsInvalidForeignAndUnavailableReads(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := mustSeedApp(t, e, "customer-impact")
	other := mustSeedApp(t, e, "other-app")
	d, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: other, Kind: state.DeploymentKindImage, ImageDigest: "sha256:other"})
	if err != nil {
		t.Fatal(err)
	}
	store := &routeCustomerTestStore{MemStore: e.store}
	e.s.store = store
	for _, tc := range []struct {
		id     string
		status int
	}{{"", 400}, {"bad", 400}, {uuid.Nil.String(), 400}, {uuid.NewString(), 404}, {d.ID, 404}} {
		res := e.do(t, "GET", "/v1/apps/customer-impact/analytics/route-customers?deployment_id="+tc.id, nil, nil)
		if res.Code != tc.status {
			t.Fatalf("id %q: %d %s", tc.id, res.Code, res.Body.String())
		}
	}
	if store.calls != 0 {
		t.Fatal("invalid scope reached telemetry")
	}
	d, err = e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app, Kind: state.DeploymentKindImage, ImageDigest: "sha256:test"})
	if err != nil {
		t.Fatal(err)
	}
	store.err = errors.New("secret database diagnostic")
	res := e.do(t, "GET", "/v1/apps/customer-impact/analytics/route-customers?deployment_id="+d.ID, nil, nil)
	if res.Code != 503 || strings.Contains(res.Body.String(), "secret") {
		t.Fatalf("%d: %s", res.Code, res.Body.String())
	}
}

func TestRouteCustomerUsagePlanAndAccountBoundary(t *testing.T) {
	for _, plan := range []api.Plan{api.PlanFree, api.PlanPro} {
		t.Run(string(plan), func(t *testing.T) {
			e := setup(t, plan)
			foreign, err := e.store.CreateAccount(t.Context(), "foreign@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.store.CreateApp(t.Context(), state.App{AccountID: foreign.ID, Slug: "foreign-app", Type: state.AppTypeApp}); err != nil {
				t.Fatal(err)
			}
			store := &routeCustomerTestStore{MemStore: e.store}
			e.s.store = store
			res := e.do(t, "GET", "/v1/apps/foreign-app/analytics/route-customers?deployment_id="+uuid.NewString(), nil, nil)
			want := 404
			if plan == api.PlanFree {
				want = 402
			}
			if res.Code != want || store.calls != 0 {
				t.Fatalf("%d: %s; queries %d", res.Code, res.Body.String(), store.calls)
			}
		})
	}
}

func TestRouteCustomerWindowAnchorsHistoricalRetentionToNow(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	r := httptest.NewRequest("GET", "/?since=7d&until=2026-10-02T12:00:00Z", nil)
	w, err := routeCustomerWindow(r, now, 3*24*time.Hour)
	if err != nil || !w.From.Equal(now.Add(-3*24*time.Hour)) || !w.WindowClamped {
		t.Fatalf("%+v: %v", w, err)
	}
	for _, query := range []string{"?until=2026-09-01T00:00:00Z", "?until=2026-10-04T00:00:00Z", "?since=bad"} {
		if _, err := routeCustomerWindow(httptest.NewRequest("GET", "/"+query, nil), now, 3*24*time.Hour); err == nil {
			t.Fatalf("accepted %s", query)
		}
	}
}
