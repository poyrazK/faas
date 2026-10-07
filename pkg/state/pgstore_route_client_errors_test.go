package state_test

import (
	"errors"
	"fmt"
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

func TestPgWatchedClientErrorsCustomerRegressionAndAdvisoryGate(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	a, app, stable, candidate := healthFixture(t, s)
	selectors := []api.RouteHealthRoute{{Method: "POST", Path: "/checkout", WatchStatuses: []int{403, 422}}, {Method: "POST", Path: "/validation", WatchStatuses: []int{422, 429}}, {Method: "GET", Path: "/auth", WatchStatuses: []int{403}}, {Method: "GET", Path: "/many", WatchStatuses: []int{403}}}
	zero := int64(0)
	g, err := s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "enforce", ExpectedRevision: &zero, Routes: selectors})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE route_health_gates SET updated_at=clock_timestamp()-interval '1 hour' WHERE app_id=$1", app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE deployments SET canary_step_started_at=clock_timestamp()-interval '1 hour',rollout_state='rolling_out' WHERE id=$1", candidate.ID); err != nil {
		t.Fatal(err)
	}
	tenant, _, err := s.CreatePlatformTenant(t.Context(), a.ID, "affected", "Affected", 10)
	if err != nil {
		t.Fatal(err)
	}
	current, _, err := s.CreatePlatformTenant(t.Context(), a.ID, "current", "Current", 10)
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := s.CreateAPIConsumer(t.Context(), a.ID, app.ID, "affected", "Affected")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LinkPlatformTenantConsumer(t.Context(), a.ID, current.ID, consumer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RevokeAPIConsumer(t.Context(), a.ID, consumer.ID); err != nil {
		t.Fatal(err)
	}
	healthy, _, err := s.CreatePlatformTenant(t.Context(), a.ID, "healthy", "Healthy", 10)
	if err != nil {
		t.Fatal(err)
	}
	q := &sqlc.Queries{}
	windows := routehealth.Windows(time.Now())
	insert := func(dep, route, method, consumerID, tenantID string, count, status int32, at time.Time) {
		t.Helper()
		p := sqlc.InsertRequestTelemetryParams{AccountID: mustPgUUID(t, a.ID), AppID: mustPgUUID(t, app.ID), DeploymentID: mustPgUUID(t, dep), Route: method + " " + route, Method: method, Status: status, LatencyMs: 100, Count: count, ReceivedAt: state.NewPgtypeTime(at), UaFamily: "__unknown__", ReferrerHost: "__none__", Country: "__unknown__", GuestRuntime: "__unknown__", GuestOutcome: "missing", FlagEvidenceJson: "[]"}
		if consumerID != "" {
			p.ConsumerID = mustPgUUID(t, consumerID)
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
			checkoutStatus, validationStatus := int32(200), int32(200)
			if dep == candidate.ID {
				checkoutStatus, validationStatus = 403, 422
			}
			insert(dep, "/checkout", "POST", consumer.ID, tenant.ID, 80, 200, w.Start)
			insert(dep, "/checkout", "POST", consumer.ID, tenant.ID, 20, checkoutStatus, w.Start)
			insert(dep, "/checkout", "POST", "", healthy.ID, 10000, 200, w.Start)
			insert(dep, "/validation", "POST", consumer.ID, tenant.ID, 100, validationStatus, w.Start)
			insert(dep, "/auth", "GET", consumer.ID, tenant.ID, 80, 403, w.Start)
			insert(dep, "/auth", "GET", consumer.ID, tenant.ID, 20, 200, w.Start)
			// These rows must never be joined by either advisory query.
			insert(dep, "/checkout", "GET", consumer.ID, tenant.ID, 999, 403, w.Start)
			insert(dep, "/checkout/123", "POST", consumer.ID, tenant.ID, 999, 403, w.Start)
			insert(dep, "/checkout", "POST", consumer.ID, tenant.ID, 999, 403, windows[1].End)
		}
	}
	ids := []string{}
	for i := 0; i < 23; i++ {
		c, err := s.CreateAPIConsumer(t.Context(), a.ID, app.ID, fmt.Sprintf("c-%d", i), "Consumer")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, c.ID)
		for _, w := range windows {
			for _, dep := range []string{stable.ID, candidate.ID} {
				status := int32(200)
				if i == 22 && dep == candidate.ID {
					status = 403
				}
				insert(dep, "/many", "GET", c.ID, "", 100, status, w.Start)
			}
		}
	}
	// An unrelated immutable deployment cannot contaminate either comparison.
	other, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:other"})
	if err != nil {
		t.Fatal(err)
	}
	insert(other.ID, "/checkout", "POST", consumer.ID, tenant.ID, 9999, 403, windows[0].Start)
	r, err := s.GetRouteHealthReportWithCustomers(t.Context(), a.ID, app.ID, candidate.ID, "tenant", true)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "healthy" || r.ClientErrorStatus != "regressed" || r.Customers.Status != "regressed" {
		t.Fatalf("advisory scope: %+v", r)
	}
	if err := routehealth.ValidateClientErrors(r); err != nil {
		t.Fatal(err)
	}
	for _, f := range r.Routes {
		if f.Path == "/checkout" && (f.ClientErrors.Status != "healthy" || f.ClientErrors.Statuses[0].Windows[0].Candidate.Responses != 20 || f.ClientErrors.Statuses[0].Windows[0].Candidate.Requests != 10100) {
			t.Fatalf("weighted exact counts: %+v", f)
		}
		if f.Path == "/validation" && f.ClientErrors.Status != "regressed" {
			t.Fatal("422 was missed")
		}
		if f.Path == "/auth" && f.ClientErrors.Status != "healthy" {
			t.Fatal("stable expected rejections flagged")
		}
	}
	found := false
	for _, route := range r.Customers.Routes {
		if route.Path != "/checkout" {
			continue
		}
		for _, c := range route.Customers {
			if c.CustomerID == current.ID {
				t.Fatal("current tenant was inferred")
			}
			if c.CustomerID != tenant.ID {
				continue
			}
			found = true
			if c.Health.Status != "healthy" || c.Health.ClientErrors.Status != "regressed" {
				t.Fatalf("masked customer regression: %+v", c)
			}
			check := r
			check.Routes = []api.RouteHealthFinding{c.Health}
			check.ClientErrorStatus, check.ClientErrorReason = c.Health.ClientErrors.Status, c.Health.ClientErrors.Reason
			if err := routehealth.ValidateClientErrors(check); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !found {
		t.Fatal("historical revoked consumer attribution missing")
	}
	consumers, err := s.GetRouteHealthReportWithCustomers(t.Context(), a.ID, app.ID, candidate.ID, "consumer", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range consumers.Customers.Routes {
		for _, c := range route.Customers {
			if c.CustomerID != "" {
				t.Fatal("default identity leakage")
			}
		}
	}
	detailed, err := s.GetRouteHealthReportWithCustomers(t.Context(), a.ID, app.ID, candidate.ID, "consumer", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range detailed.Customers.Routes {
		if route.Path == "/many" {
			if route.ObservedCustomers != 23 || !route.CustomersTruncated || route.Customers[0].CustomerID != ids[22] || route.Customers[0].Health.ClientErrors.Status != "regressed" || route.Candidate.OtherCustomerRequests != 600 {
				t.Fatalf("4xx cap ranking: %+v", route)
			}
		}
	}
	if _, err := s.GetRouteHealthReport(t.Context(), uuid.NewString(), app.ID, candidate.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("cross-account read")
	}
	var decision api.RouteHealthDecision
	_, _, err = s.AdvanceCanary(t.Context(), candidate.ID, state.CanaryAdvanceParams{ExpectedStep: 0, TrafficPercent: 10, RouteHealthDecision: &decision, Audit: state.DeploymentAudit{Kind: state.DeployTrafficChanged, Actor: "test"}})
	if err != nil || decision.Status != "allowed" {
		t.Fatalf("4xx changed gate: %+v %v", decision, err)
	}
	history, err := s.ListRouteHealthHistory(t.Context(), a.ID, app.ID, candidate.ID, 5, "")
	if err != nil || len(history.Entries) == 0 {
		t.Fatalf("history: %v", err)
	}
	if history.Entries[0].Report.ClientErrorStatus != "" {
		t.Fatal("advisory persisted into history")
	}
	for _, f := range history.Entries[0].Report.Routes {
		if f.ClientErrors != nil {
			t.Fatal("advisory code evidence persisted")
		}
	}
	// A watch-only configuration edit is revisioned and requires fresh windows.
	selectors[0].WatchStatuses = []int{404}
	updated, err := s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "enforce", ExpectedRevision: &g.Revision, Routes: selectors})
	if err != nil || updated.Revision != g.Revision+1 {
		t.Fatal("watch selector revision fence")
	}
	fresh, err := s.GetRouteHealthReport(t.Context(), a.ID, app.ID, candidate.ID)
	if err != nil || fresh.ClientErrorStatus != "unknown" {
		t.Fatal("old windows reused after selector update")
	}
	if _, err := pool.Exec(t.Context(), "UPDATE accounts SET plan='free' WHERE id=$1", a.ID); err != nil {
		t.Fatal(err)
	}
	unavailable, err := s.GetRouteHealthReport(t.Context(), a.ID, app.ID, candidate.ID)
	if err != nil || unavailable.ClientErrorStatus != "unknown" || unavailable.StableDeploymentID != "" {
		t.Fatal("entitlement bypass")
	}
	for _, f := range unavailable.Routes {
		for _, signal := range f.ClientErrors.Statuses {
			for _, w := range signal.Windows {
				if w.Candidate.Requests != 0 || w.Stable.Requests != 0 {
					t.Fatal("non-entitled counts leaked")
				}
			}
		}
	}
}

func TestWatchStatusMemStoreDeepCopiesIntent(t *testing.T) {
	s := state.NewMemStore()
	a, app, _, _ := healthFixture(t, s)
	zero := int64(0)
	selectors := []api.RouteHealthRoute{{Method: "GET", Path: "/auth", WatchStatuses: []int{403}}}
	g, err := s.SetRouteHealthGate(t.Context(), a.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &zero, Routes: selectors})
	if err != nil {
		t.Fatal(err)
	}
	selectors[0].WatchStatuses[0] = 422
	g.Routes[0].WatchStatuses[0] = 404
	current, err := s.GetRouteHealthGate(t.Context(), a.ID, app.ID)
	if err != nil || current.Routes[0].WatchStatuses[0] != 403 {
		t.Fatal("caller mutated saved intent")
	}
}
