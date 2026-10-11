package routestatus

import (
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func usage(method, path string, requests, tenants int64) api.RouteCustomerUsage {
	return api.RouteCustomerUsage{Route: method + " " + path, Method: method, Requests: requests, PlatformTenantCount: tenants, LastObservedAt: "2026-10-09T12:00:00Z"}
}

func TestOperationsFromDoc(t *testing.T) {
	doc := map[string]any{"paths": map[string]any{
		"/users/{userId}": map[string]any{"get": map[string]any{"operationId": "getUser"}, "parameters": []any{}},
		"/checkout":       map[string]any{"post": map[string]any{}, "trace": map[string]any{}},
	}}
	got := OperationsFromDoc(doc)
	want := []Operation{{Method: "POST", Path: "/checkout"}, {Method: "GET", Path: "/users/{userId}", OperationID: "getUser"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OperationsFromDoc = %+v, want %+v", got, want)
	}
	if got := OperationsFromDoc(map[string]any{}); got == nil || len(got) != 0 {
		t.Fatalf("empty doc = %#v", got)
	}
}

func TestBuildJoinsProtectionsAndGaps(t *testing.T) {
	budget := int64(100)
	opened := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	in := Inputs{
		App: "api", ServingDeploymentID: "d1", CandidateDeployment: "d2", Since: "168h", ContractCaptured: true,
		Contract: []Operation{
			{Method: "POST", Path: "/checkout", OperationID: "checkout"},
			{Method: "GET", Path: "/users/{userId}", OperationID: "getUser"},
			{Method: "DELETE", Path: "/legacy"},
		},
		Usage: &api.RouteCustomerUsageResponse{Routes: []api.RouteCustomerUsage{
			usage("POST", "/checkout", 900, 40),
			usage("GET", "/users/{id}", 5000, 12),
			usage("GET", "/debug", 50, 1),
			usage("GET", "/search", 300, 30),
		}},
		HealthGate:   &api.RouteHealthGate{Mode: "enforce", Routes: []api.RouteHealthRoute{{Method: "GET", Path: "/users/{id}"}}},
		HealthReport: &api.RouteHealthReport{Routes: []api.RouteHealthFinding{{Method: "GET", Path: "/users/{id}", Status: "healthy", EvidenceWindow: "pooled"}}},
		Monitor: &api.RouteMonitorConfig{Enabled: true, OnViolation: "rollback", Routes: []api.RouteMonitorRoute{
			{Method: "POST", Path: "/checkout", Max5xxRateBPS: &budget},
			{Method: "GET", Path: "/search", MaxP95MS: 300},
		}},
		MonitorReport:  &api.RouteMonitorReport{Routes: []api.RouteMonitorFinding{{Route: api.RouteMonitorRoute{Method: "POST", Path: "/checkout"}, Status: "violated"}}},
		LatestIncident: &api.RouteMonitorIncident{ID: "inc", Status: "open", OpenedAt: opened, Rollback: &api.RouteMonitorIncidentRollback{Status: "requested", OperationID: "op"}},
		Requirements:   &api.SavedRouteRequirements{Requirements: api.RouteRequirementsConfig{Routes: []api.RouteRequirement{{Method: "get", Path: "/debug"}}}},
	}
	s := Build(in)
	byKey := map[string]Route{}
	order := []string{}
	for _, r := range s.Routes {
		byKey[r.Method+" "+r.Path] = r
		order = append(order, r.Method+" "+r.Path)
	}
	if want := []string{"POST /checkout", "GET /search", "GET /users/{id}", "GET /debug", "DELETE /legacy"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v (tenants, then requests)", order, want)
	}
	checkout := byKey["POST /checkout"]
	if checkout.Protection != "rollback" || !checkout.InContract || checkout.OperationID != "checkout" || checkout.Production.Status != "violated" || len(checkout.Gaps) != 0 {
		t.Errorf("checkout = %+v", checkout)
	}
	users := byKey["GET /users/{id}"]
	if users.Protection != "enforced" || users.ContractPath != "/users/{userId}" || users.Canary.Status != "healthy" || users.Canary.Evidence != "pooled" {
		t.Errorf("users = %+v (canary %+v)", users, users.Canary)
	}
	search := byKey["GET /search"]
	if search.Protection != "monitored" || !reflect.DeepEqual(search.Gaps, []string{GapOutsideContract, GapRollbackNoErrorCap}) {
		t.Errorf("search = %+v", search)
	}
	debug := byKey["GET /debug"]
	if debug.Protection != "policy_only" || !reflect.DeepEqual(debug.Gaps, []string{GapUnprotected, GapOutsideContract}) {
		t.Errorf("debug = %+v", debug)
	}
	legacy := byKey["DELETE /legacy"]
	if legacy.Protection != "none" || !reflect.DeepEqual(legacy.Gaps, []string{GapNoTraffic}) {
		t.Errorf("legacy = %+v", legacy)
	}
	if s.Summary != (Summary{Routes: 5, Protected: 3, Unprotected: 1, Rollback: 1}) {
		t.Errorf("summary = %+v", s.Summary)
	}
	if s.LatestIncident == nil || s.LatestIncident.Rollback.OperationID != "op" || s.LatestIncident.OpenedAt != "2026-10-09T12:00:00Z" {
		t.Errorf("incident = %+v", s.LatestIncident)
	}
}

func TestBuildAmbiguousShapeAndUnavailableSections(t *testing.T) {
	in := Inputs{
		App: "api", ContractCaptured: true,
		Contract: []Operation{{Method: "GET", Path: "/items/{itemId}"}},
		Usage: &api.RouteCustomerUsageResponse{Routes: []api.RouteCustomerUsage{
			usage("GET", "/items/{id}", 10, 1),
			usage("GET", "/items/{slug}", 10, 1),
		}},
		Unavailable: map[string]string{"monitor": "request telemetry is not included in this plan"},
	}
	s := Build(in)
	if len(s.Routes) != 3 {
		t.Fatalf("ambiguous shape merged rows: %+v", s.Routes)
	}
	for _, r := range s.Routes {
		if r.Path == "/items/{itemId}" && (!r.InContract || !reflect.DeepEqual(r.Gaps, []string{GapNoTraffic})) {
			t.Errorf("contract row = %+v", r)
		}
		if r.Path != "/items/{itemId}" && (r.InContract || r.ContractPath != "") {
			t.Errorf("observed row joined an ambiguous contract operation: %+v", r)
		}
	}
	if s.Unavailable["monitor"] == "" {
		t.Error("unavailable section was dropped")
	}
	if empty := Build(Inputs{App: "api"}); empty.Routes == nil || len(empty.Routes) != 0 || empty.Summary.Routes != 0 {
		t.Errorf("empty build = %+v", empty)
	}
}
