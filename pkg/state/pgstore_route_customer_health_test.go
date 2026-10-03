package state_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgCustomerHealthMaskedRegressionAndHistoricalAttribution(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	a, app, stable, candidate := healthFixture(t, s)
	zero := int64(0)
	_, err := s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "enforce", ExpectedRevision: &zero, Routes: []api.RouteHealthRoute{{Method: "POST", Path: "/checkout", CheckLatency: true}, {Method: "GET", Path: "/unseen"}}})
	if err != nil {
		t.Fatal(err)
	}
	// Avoid wall-clock boundaries and force mature observation anchors.
	if _, err := pool.Exec(t.Context(), "UPDATE deployments SET canary_step_started_at = clock_timestamp() - interval '1 hour', rollout_state = 'rolling_out' WHERE id = $1", candidate.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE route_health_gates SET updated_at = clock_timestamp() - interval '1 hour' WHERE app_id = $1", app.ID); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := s.CreatePlatformTenant(t.Context(), a.ID, "historical", "Historical", 10)
	if err != nil {
		t.Fatal(err)
	}
	otherTenant, _, err := s.CreatePlatformTenant(t.Context(), a.ID, "current", "Current", 10)
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := s.CreateAPIConsumer(t.Context(), a.ID, app.ID, "consumer", "Consumer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LinkPlatformTenantConsumer(t.Context(), a.ID, otherTenant.ID, consumer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RevokeAPIConsumer(t.Context(), a.ID, consumer.ID); err != nil {
		t.Fatal(err)
	}
	healthy, _, err := s.CreatePlatformTenant(t.Context(), a.ID, "healthy", "Healthy", 10)
	if err != nil {
		t.Fatal(err)
	}
	foreignAccount, _, _, _ := healthFixture(t, s)
	foreignTenant, _, err := s.CreatePlatformTenant(t.Context(), foreignAccount.ID, "foreign", "Foreign", 10)
	if err != nil {
		t.Fatal(err)
	}
	q := &sqlc.Queries{}
	windows := routehealth.Windows(time.Now())
	insert := func(dep, customer, tenantID, route, method string, count, status, latency int32, at time.Time) {
		t.Helper()
		p := sqlc.InsertRequestTelemetryParams{AccountID: mustPgUUID(t, a.ID), AppID: mustPgUUID(t, app.ID), DeploymentID: mustPgUUID(t, dep), Route: route, Method: method, Status: status, LatencyMs: latency, ReceivedAt: state.NewPgtypeTime(at), Count: count, UaFamily: "__unknown__", ReferrerHost: "__none__", Country: "__unknown__", GuestRuntime: "__unknown__", GuestOutcome: "missing", FlagEvidenceJson: "[]"}
		if customer != "" {
			p.ConsumerID = mustPgUUID(t, customer)
		}
		if tenantID != "" {
			p.PlatformTenantID = mustPgUUID(t, tenantID)
		}
		if err := q.InsertRequestTelemetry(t.Context(), pool, p); err != nil {
			t.Fatal(err)
		}
	}
	for _, w := range windows {
		for _, dep := range []string{stable.ID, candidate.ID} {
			insert(dep, "", healthy.ID, "POST /checkout", "POST", 10000, 200, 100, w.Start.Add(time.Second))
			insert(dep, "", "", "POST /checkout", "POST", 7, 200, 100, w.Start.Add(time.Second))
			insert(dep, "", foreignTenant.ID, "POST /checkout", "POST", 3, 200, 100, w.Start.Add(time.Second))
			status, latency := int32(200), int32(100)
			if dep == candidate.ID {
				status, latency = 500, 1000
			}
			insert(dep, consumer.ID, tenant.ID, "POST /checkout", "POST", 20, status, latency, w.Start.Add(time.Second))
			insert(dep, consumer.ID, tenant.ID, "POST /checkout", "POST", 80, 200, 100, w.Start.Add(time.Second))
			// Neither a label/method mismatch nor an expanded URL belongs to a selected cohort.
			insert(dep, consumer.ID, tenant.ID, "POST /checkout", "GET", 999, 500, 1000, w.Start.Add(time.Second))
			insert(dep, consumer.ID, tenant.ID, "POST /checkout/123", "POST", 999, 500, 1000, w.Start.Add(time.Second))
		}
	}
	report, err := s.GetRouteHealthReportWithCustomers(t.Context(), a.ID, app.ID, candidate.ID, "tenant", true)
	if err != nil {
		t.Fatal(err)
	}
	// The unseen route is unknown; the busy checkout itself stays healthy.
	if report.Routes[0].Status != "healthy" || report.Customers.Status != "regressed" {
		t.Fatalf("masked regression: %+v", report)
	}
	var checkout api.RouteCustomerHealthRoute
	for _, r := range report.Customers.Routes {
		if r.Path == "/checkout" {
			checkout = r
		}
	}
	if checkout.ObservedCustomers != 2 || checkout.Candidate.IdentifiedRequests != 20200 || checkout.Candidate.UnattributedRequests != 14 || checkout.Candidate.UnresolvedIdentityRequests != 6 {
		t.Fatalf("scope/weights: %+v", checkout)
	}
	found := false
	for _, c := range checkout.Customers {
		if c.CustomerID == foreignTenant.ID || c.CustomerID == otherTenant.ID {
			t.Fatal("foreign/current tenant was inferred")
		}
		if c.CustomerID != tenant.ID {
			continue
		}
		found = true
		if c.Health.Status != "regressed" || c.Health.LatencyStatus != "regressed" {
			t.Fatalf("cohort: %+v", c)
		}
		if math.Abs(*c.Health.Windows[0].Candidate.P95LatencyMS-1000) > 1e-9 || c.Health.Windows[0].Candidate.Requests != 100 {
			t.Fatal("weighted latency/count")
		}
	}
	if !found {
		t.Fatal("historical tenant missing")
	}
	redacted, err := s.GetRouteHealthReportWithCustomers(t.Context(), a.ID, app.ID, candidate.ID, "consumer", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range redacted.Customers.Routes {
		for _, c := range r.Customers {
			if c.CustomerID != "" {
				t.Fatal("default identity leakage")
			}
		}
	}
	detailed, err := s.GetRouteHealthReportWithCustomers(t.Context(), a.ID, app.ID, candidate.ID, "consumer", true)
	if err != nil {
		t.Fatal(err)
	}
	if detailed.Customers.Routes[1].Customers[0].CustomerID != consumer.ID {
		t.Fatalf("revoked consumer omitted: %+v", detailed.Customers)
	}
	if _, err := s.GetRouteHealthReportWithCustomers(t.Context(), foreignAccount.ID, app.ID, candidate.ID, "tenant", true); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross-account read")
	}
	if _, err := s.GetRouteHealthReportWithCustomers(t.Context(), a.ID, app.ID, uuid.NewString(), "tenant", true); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross-deployment read")
	}
	if _, err := s.GetRouteHealthReportWithCustomers(t.Context(), a.ID, app.ID, candidate.ID, "arbitrary", true); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal("dimension validation")
	}
	plain, err := s.GetRouteHealthReport(t.Context(), a.ID, app.ID, candidate.ID)
	if err != nil || plain.Customers != nil {
		t.Fatal("normal reads acquired customer data")
	}
	// Remove the unseen selector so the unchanged aggregate gate can advance,
	// despite the advisory confirmed tenant regression.
	revision := report.Revision
	_, err = s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "enforce", ExpectedRevision: &revision, Routes: []api.RouteHealthRoute{{Method: "POST", Path: "/checkout", CheckLatency: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE route_health_gates SET updated_at = clock_timestamp() - interval '1 hour' WHERE app_id = $1", app.ID); err != nil {
		t.Fatal(err)
	}
	var decision api.RouteHealthDecision
	_, _, err = s.AdvanceCanary(t.Context(), candidate.ID, state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 10, RouteHealthDecision: &decision, Audit: state.DeploymentAudit{Kind: state.DeployTrafficChanged, Actor: "test"}})
	if err != nil || decision.Status != "allowed" {
		t.Fatalf("advisory affected gate: %+v %v", decision, err)
	}
	history, err := s.ListRouteHealthHistory(t.Context(), a.ID, app.ID, candidate.ID, 5, "")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(history)
	if len(history.Entries) == 0 || history.Entries[0].Report.Customers != nil {
		t.Fatalf("advisory was retained: %s", body)
	}
}

func TestPgCustomerHealthBoundedUnionAndWeightedP95(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	a, app, stable, candidate := healthFixture(t, s)
	windows := routehealth.Windows(time.Now())
	routes, _ := json.Marshal([]api.RouteHealthRoute{{Method: "GET", Path: "/many", CheckLatency: true}, {Method: "GET", Path: "/unseen"}})
	windowJSON, _ := json.Marshal(windows)
	q := &sqlc.Queries{}
	ids := []string{}
	for i := 0; i < 23; i++ {
		c, err := s.CreateAPIConsumer(t.Context(), a.ID, app.ID, fmt.Sprintf("c-%d", i), "Consumer")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, c.ID)
		for _, w := range windows {
			for _, dep := range []string{stable.ID, candidate.ID} {
				if i == 22 && dep == stable.ID {
					continue
				} // union includes candidate-only identity
				for _, row := range []struct{ count, latency int32 }{{95, 100}, {5, 1000}} {
					p := sqlc.InsertRequestTelemetryParams{AccountID: mustPgUUID(t, a.ID), AppID: mustPgUUID(t, app.ID), DeploymentID: mustPgUUID(t, dep), ConsumerID: mustPgUUID(t, c.ID), Route: "GET /many", Method: "GET", Status: 200, LatencyMs: row.latency, Count: row.count, ReceivedAt: state.NewPgtypeTime(w.Start), UaFamily: "__unknown__", ReferrerHost: "__none__", Country: "__unknown__", GuestRuntime: "__unknown__", GuestOutcome: "missing", FlagEvidenceJson: "[]"}
					if i == 22 {
						p.Status = 500
					}
					if err := q.InsertRequestTelemetry(t.Context(), pool, p); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
	}
	arg := sqlc.RouteCustomerHealthObservationParams{AppID: app.ID, AccountID: a.ID, CandidateID: candidate.ID, StableID: stable.ID, GroupBy: "consumer", Routes: routes, Windows: windowJSON, Since: state.NewPgtypeTime(windows[0].Start), Until: state.NewPgtypeTime(windows[1].End), CustomerLimit: 20, LatencyQuantile: api.RouteHealthLatencyQuantile}
	body, err := q.RouteCustomerHealthObservation(t.Context(), pool, arg)
	if err != nil {
		t.Fatal(err)
	}
	var rows []api.RouteCustomerHealthRoute
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatal(err)
	}
	r := rows[0]
	if r.ObservedCustomers != 23 || !r.CustomersTruncated || len(r.Customers) != 20 || r.Candidate.IdentifiedRequests != 4600 || r.Candidate.OtherCustomerRequests != 600 || r.Stable.IdentifiedRequests != 4400 || r.Stable.OtherCustomerRequests != 600 {
		t.Fatalf("cap/weights: %+v", r)
	}
	if r.Customers[0].CustomerID != ids[22] || r.Customers[0].Health.Windows[0].Stable.Requests != 0 {
		t.Fatal("candidate-only failing cohort hidden")
	}
	if math.Abs(*r.Customers[1].Health.Windows[0].Candidate.P95LatencyMS-145) > 1e-9 {
		t.Fatal("weighted interpolation")
	}
	for i := 2; i < len(r.Customers); i++ {
		if r.Customers[i-1].CustomerID >= r.Customers[i].CustomerID {
			t.Fatal("unstable tie ordering")
		}
	}
	if rows[1].ObservedCustomers != 0 || len(rows[1].Customers) != 0 {
		t.Fatal("unseen route omitted")
	}
	arg.AccountID = uuid.NewString()
	body, err = q.RouteCustomerHealthObservation(t.Context(), pool, arg)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(body, &rows); err != nil || rows[0].ObservedCustomers != 0 {
		t.Fatal("query ownership isolation")
	}
}
