package state_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgRouteInvestigationExactWindowsWeightedExamplesAndCustomerScope(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	a, app, stable, candidate := healthFixture(t, s)
	zero := int64(0)
	_, err := s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "enforce", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{{Method: "POST", Path: "/checkout", WatchStatuses: []int{403, 422}, CheckLatency: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE route_health_gates SET updated_at=clock_timestamp()-interval '1 hour' WHERE app_id=$1", app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE deployments SET canary_step_started_at=clock_timestamp()-interval '1 hour',rollout_state='rolling_out' WHERE id=$1", candidate.ID); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := s.CreatePlatformTenant(t.Context(), a.ID, "historical", "Historical", 30)
	if err != nil {
		t.Fatal(err)
	}
	current, _, err := s.CreatePlatformTenant(t.Context(), a.ID, "current", "Current", 30)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateAPIConsumer(t.Context(), a.ID, app.ID, "consumer", "Consumer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LinkPlatformTenantConsumer(t.Context(), a.ID, current.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RevokeAPIConsumer(t.Context(), a.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	windows := routehealth.Windows(time.Now())
	q := &sqlc.Queries{}
	insert := func(dep, tenantID, consumerID, method, path string, status, count int32, at time.Time, trace string) {
		t.Helper()
		p := sqlc.InsertRequestTelemetryParams{AccountID: mustPgUUID(t, a.ID), AppID: mustPgUUID(t, app.ID), DeploymentID: mustPgUUID(t, dep), Route: method + " " + path, Method: method, Status: status, Count: count, LatencyMs: 100, ReceivedAt: state.NewPgtypeTime(at), TraceID: pgtype.Text{String: trace, Valid: trace != ""}, UaFamily: "__unknown__", ReferrerHost: "__none__", Country: "__unknown__", GuestRuntime: "__unknown__", GuestOutcome: "missing", FlagEvidenceJson: "[]"}
		if tenantID != "" {
			p.PlatformTenantID = mustPgUUID(t, tenantID)
		}
		if consumerID != "" {
			p.ConsumerID = mustPgUUID(t, consumerID)
		}
		if err := q.InsertRequestTelemetry(t.Context(), pool, p); err != nil {
			t.Fatal(err)
		}
	}
	for _, w := range windows {
		insert(candidate.ID, tenant.ID, c.ID, "POST", "/checkout", 200, 80, w.Start, "")
		insert(stable.ID, tenant.ID, c.ID, "POST", "/checkout", 200, 99, w.Start, "")
		insert(stable.ID, tenant.ID, c.ID, "POST", "/checkout", 403, 1, w.Start, "")
		for i := 0; i < 4; i++ {
			trace := ""
			if i == 0 {
				trace = "4bf92f3577b34da6a3ce929d0e0e4736"
			}
			insert(candidate.ID, tenant.ID, c.ID, "POST", "/checkout", 403, 5, w.Start.Add(time.Duration(i)*time.Second), trace)
		}
		for _, dep := range []string{candidate.ID, stable.ID} {
			insert(dep, "", "", "POST", "/checkout", 200, 10000, w.Start, "")
			insert(dep, tenant.ID, c.ID, "GET", "/checkout", 403, 999, w.Start, "")
			insert(dep, tenant.ID, c.ID, "POST", "/checkout/123", 403, 999, w.Start, "")
			insert(dep, tenant.ID, c.ID, "POST", "/checkout", 403, 999, windows[1].End, "")
		}
	}
	// Candidate 5xx priority puts the affected 4xx cohort outside the usual cap.
	for i := 0; i < 22; i++ {
		tenant, _, err := s.CreatePlatformTenant(t.Context(), a.ID, fmt.Sprintf("other-%d", i), "Other", 30)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range windows {
			insert(candidate.ID, tenant.ID, "", "POST", "/checkout", 500, 1, w.Start, "")
			insert(stable.ID, tenant.ID, "", "POST", "/checkout", 200, 1, w.Start, "")
		}
	}
	other, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:other", TrafficPercent: 1, CanaryPreset: "balanced", CanaryTotalSteps: 4, RolloutState: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	insert(other.ID, tenant.ID, c.ID, "POST", "/checkout", 403, 999, windows[0].Start, "")
	capped, err := s.GetRouteHealthReportWithCustomers(t.Context(), a.ID, app.ID, candidate.ID, "tenant", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, cohort := range capped.Customers.Routes[0].Customers {
		if cohort.CustomerID == tenant.ID {
			t.Fatal("fixture did not place target outside cohort cap")
		}
	}
	opts := api.RouteHealthInvestigationOptions{Method: "POST", Path: "/checkout", StatusCode: 403, CustomerID: tenant.ID}
	r, err := s.GetRouteHealthInvestigation(t.Context(), a.ID, app.ID, candidate.ID, opts)
	if err != nil {
		t.Fatal(err)
	}
	if r.Report.Status != "healthy" || r.Status != "regressed" || r.Finding.Status != "healthy" || r.Selection.CustomerGroupBy != "tenant" || r.Report.Customers != nil || r.EvidenceStatus != "observed" {
		t.Fatalf("scope and signal: %+v", r)
	}
	if err := routehealth.ValidateInvestigation(r, opts, app.Slug); err != nil {
		t.Fatal(err)
	}
	for _, w := range r.Windows {
		if w.Candidate.MatchingRequests != 20 || w.Candidate.ObservedRows != 4 || !w.Candidate.ExamplesTruncated || len(w.Candidate.Examples) != 3 || w.Candidate.Examples[0].TraceID == "" || w.Stable.MatchingRequests != 1 || w.Stable.ExamplesTruncated {
			t.Fatalf("weighted bounded examples: %+v", w)
		}
	}
	if r.Finding.Windows[0].Candidate.Requests != 100 || r.Finding.Windows[0].Candidate.P95LatencyMS == nil || *r.Finding.Windows[0].Candidate.P95LatencyMS != 100 {
		t.Fatal("customer denominator or optional latency evidence lost")
	}
	opts.CustomerGroupBy, opts.CustomerID = "consumer", c.ID
	consumer, err := s.GetRouteHealthInvestigation(t.Context(), a.ID, app.ID, candidate.ID, opts)
	if err != nil || consumer.Status != "regressed" || routehealth.ValidateInvestigation(consumer, opts, app.Slug) != nil {
		t.Fatalf("revoked consumer: %v", err)
	}
	opts.CustomerGroupBy, opts.CustomerID = "tenant", current.ID
	missing, err := s.GetRouteHealthInvestigation(t.Context(), a.ID, app.ID, candidate.ID, opts)
	if err != nil || missing.Status != "unknown" || missing.Windows[0].Candidate.MatchingRequests != 0 {
		t.Fatal("current tenant link rewrote historical attribution")
	}
	opts.CustomerGroupBy, opts.CustomerID, opts.StatusCode = "", "", 0
	failures, err := s.GetRouteHealthInvestigation(t.Context(), a.ID, app.ID, candidate.ID, opts)
	if err != nil || failures.Windows[0].Candidate.MatchingRequests != 22 || failures.Windows[0].Candidate.ObservedRows != 22 || failures.Windows[0].Stable.MatchingRequests != 0 || routehealth.ValidateInvestigation(failures, opts, app.Slug) != nil {
		t.Fatalf("5xx selection: %+v %v", failures, err)
	}
	for _, invalid := range []api.RouteHealthInvestigationOptions{{Method: "GET", Path: "/checkout"}, {Method: "POST", Path: "/checkout", StatusCode: 404}, {Method: "POST", Path: "/checkout", CustomerID: uuid.NewString()}, {Method: "POST", Path: "/checkout", CustomerGroupBy: "tenant"}} {
		if _, err := s.GetRouteHealthInvestigation(t.Context(), a.ID, app.ID, candidate.ID, invalid); err == nil {
			t.Fatal("invalid selection accepted")
		}
	}
	if _, err := s.GetRouteHealthInvestigation(t.Context(), uuid.NewString(), app.ID, candidate.ID, opts); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross-account read")
	}
	foreign, err := s.CreateAccount(t.Context(), uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreignTenant, _, err := s.CreatePlatformTenant(t.Context(), foreign.ID, "foreign", "Foreign", 10)
	if err != nil {
		t.Fatal(err)
	}
	opts.CustomerID = foreignTenant.ID
	if _, err := s.GetRouteHealthInvestigation(t.Context(), a.ID, app.ID, candidate.ID, opts); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign tenant accepted")
	}
	opts.CustomerID = ""
	history, err := s.ListRouteHealthHistory(t.Context(), a.ID, app.ID, candidate.ID, 5, "")
	if err != nil || len(history.Entries) != 0 {
		t.Fatal("read persisted decision state")
	}
	d, err := s.DeploymentByID(t.Context(), candidate.ID)
	if err != nil || d.TrafficPercent != 1 || d.CanaryStep != 0 {
		t.Fatal("read changed canary traffic")
	}
	if err := s.MarkDeploymentLive(t.Context(), other.ID); err != nil {
		t.Fatal(err)
	}
	ambiguous, err := s.GetRouteHealthInvestigation(t.Context(), a.ID, app.ID, candidate.ID, opts)
	if err != nil || ambiguous.EvidenceStatus != "unavailable" || ambiguous.Report.StableDeploymentID != "" || ambiguous.Windows[0].Candidate.MatchingRequests != 0 || routehealth.ValidateInvestigation(ambiguous, opts, app.Slug) != nil {
		t.Fatalf("ambiguous comparison invented examples: %+v %v", ambiguous, err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE accounts SET plan='free' WHERE id=$1", a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetRouteHealthInvestigation(t.Context(), a.ID, app.ID, candidate.ID, opts); !errors.Is(err, state.ErrRouteInvestigationPlan) {
		t.Fatal("request telemetry entitlement bypassed")
	}
}
