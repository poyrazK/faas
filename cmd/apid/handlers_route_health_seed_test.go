package main

import (
	"context"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// seedTelemetryStore supplies retained route usage, which MemStore does not keep.
type seedTelemetryStore struct {
	*state.MemStore
	rows  []sqlc.RequestTelemetryRouteCustomersRow
	reads []string
}

func (s *seedTelemetryStore) RequestTelemetryRouteCustomers(_ context.Context, arg sqlc.RequestTelemetryRouteCustomersParams) ([]sqlc.RequestTelemetryRouteCustomersRow, error) {
	s.reads = append(s.reads, uuidFromPg(arg.DeploymentID))
	return s.rows, nil
}

func seedUsageRow(method, path string, tenants, requests int64) sqlc.RequestTelemetryRouteCustomersRow {
	return sqlc.RequestTelemetryRouteCustomersRow{
		Route: method + " " + path, Method: method, Requests: requests, IdentifiedRequests: requests,
		PlatformTenantCount: tenants, MatchedRoutes: 3,
	}
}

func seedCanaryFixture(t *testing.T, e testEnv, slug string) (state.App, state.Deployment, state.Deployment) {
	t.Helper()
	ctx := context.Background()
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: slug})
	if err != nil {
		t.Fatal(err)
	}
	stable, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:stable"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(ctx, stable.ID); err != nil {
		t.Fatal(err)
	}
	canary, err := e.store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:canary", CanaryPreset: "balanced",
		CanaryStep: 0, CanaryTotalSteps: 4, RolloutState: "pending", TrafficPercent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(ctx, canary.ID); err != nil {
		t.Fatal(err)
	}
	canary, err = e.store.DeploymentByID(ctx, canary.ID)
	if err != nil {
		t.Fatal(err)
	}
	return app, stable, canary
}

// adr: 844
// The first canary advance of an unconfigured app saves report-mode
// selectors ranked by tenant reach on the stable deployment.
func TestAdvanceCanarySeedsReportModeRouteHealth(t *testing.T) {
	e := setup(t, api.PlanPro)
	app, stable, canary := seedCanaryFixture(t, e, "seed-route-health")
	store := &seedTelemetryStore{MemStore: e.store, rows: []sqlc.RequestTelemetryRouteCustomersRow{
		seedUsageRow("GET", "/profiles/{id}", 2, 900),
		seedUsageRow("POST", "/checkout", 9, 40),
		seedUsageRow("POST", "/login", 9, 400),
		seedUsageRow("GET", "/search?q", 50, 50), // not an exact selector
	}}
	e.s.store = store

	rec := e.do(t, http.MethodPost, "/v1/deployments/"+canary.ID+"/canary/advance", api.AdvanceCanaryRequest{ExpectedStep: 0}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("advance status = %d, body=%s", rec.Code, rec.Body.String())
	}
	gate, err := e.store.GetRouteHealthGate(context.Background(), e.acct.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gate.Mode != "report" || gate.Revision != 1 {
		t.Fatalf("seeded gate = %+v, want report mode at revision 1", gate)
	}
	want := []api.RouteHealthRoute{{Method: "POST", Path: "/login"}, {Method: "POST", Path: "/checkout"}, {Method: "GET", Path: "/profiles/{id}"}}
	if len(gate.Routes) != len(want) {
		t.Fatalf("seeded routes = %+v, want %+v", gate.Routes, want)
	}
	for i := range want {
		if gate.Routes[i].Method != want[i].Method || gate.Routes[i].Path != want[i].Path {
			t.Fatalf("seeded routes = %+v, want %+v", gate.Routes, want)
		}
	}
	if len(store.reads) != 1 || store.reads[0] != stable.ID {
		t.Fatalf("usage reads = %v, want the stable deployment %s only", store.reads, stable.ID)
	}
}

// adr: 844
func TestSeedRouteHealthSkipsConfiguredAndIneligible(t *testing.T) {
	rows := []sqlc.RequestTelemetryRouteCustomersRow{seedUsageRow("POST", "/checkout", 3, 100)}
	ctx := context.Background()

	t.Run("customer opted out with an empty selector list", func(t *testing.T) {
		e := setup(t, api.PlanPro)
		app, _, canary := seedCanaryFixture(t, e, "seed-opt-out")
		zero := int64(0)
		if _, err := e.store.SetRouteHealthGate(ctx, e.acct.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{}}); err != nil {
			t.Fatal(err)
		}
		e.s.store = &seedTelemetryStore{MemStore: e.store, rows: rows}
		e.s.seedDefaultRouteHealthGate(ctx, e.acct, app, canary)
		if gate, _ := e.store.GetRouteHealthGate(ctx, e.acct.ID, app.ID); gate.Revision != 1 || len(gate.Routes) != 0 {
			t.Fatalf("opted-out gate changed: %+v", gate)
		}
	})
	t.Run("later canary stage", func(t *testing.T) {
		e := setup(t, api.PlanPro)
		app, _, canary := seedCanaryFixture(t, e, "seed-later-stage")
		canary.CanaryStep = 1
		e.s.store = &seedTelemetryStore{MemStore: e.store, rows: rows}
		e.s.seedDefaultRouteHealthGate(ctx, e.acct, app, canary)
		if gate, _ := e.store.GetRouteHealthGate(ctx, e.acct.ID, app.ID); gate.Revision != 0 {
			t.Fatalf("later stage seeded: %+v", gate)
		}
	})
	t.Run("no observed routes", func(t *testing.T) {
		e := setup(t, api.PlanPro)
		app, _, canary := seedCanaryFixture(t, e, "seed-no-traffic")
		e.s.store = &seedTelemetryStore{MemStore: e.store}
		e.s.seedDefaultRouteHealthGate(ctx, e.acct, app, canary)
		if gate, _ := e.store.GetRouteHealthGate(ctx, e.acct.ID, app.ID); gate.Revision != 0 {
			t.Fatalf("empty usage seeded: %+v", gate)
		}
	})
	t.Run("plan without request telemetry", func(t *testing.T) {
		e := setup(t, api.PlanFree)
		if api.MustLimitsFor(api.PlanFree).DebugTelemetryEnabled {
			t.Skip("free plan includes request telemetry")
		}
		app, _, canary := seedCanaryFixture(t, e, "seed-free")
		store := &seedTelemetryStore{MemStore: e.store, rows: rows}
		e.s.store = store
		e.s.seedDefaultRouteHealthGate(ctx, e.acct, app, canary)
		if gate, _ := e.store.GetRouteHealthGate(ctx, e.acct.ID, app.ID); gate.Revision != 0 || len(store.reads) != 0 {
			t.Fatalf("unentitled plan seeded or read telemetry: %+v reads=%v", gate, store.reads)
		}
	})
}
