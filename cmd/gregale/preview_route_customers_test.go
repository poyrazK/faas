package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const customerBaseline = "11111111-1111-4111-8111-111111111111"
const customerID = "22222222-2222-4222-8222-222222222222"
const customerTenant = "33333333-3333-4333-8333-333333333333"

func customerUsageFixture() api.RouteCustomerUsageResponse {
	return api.RouteCustomerUsageResponse{Slug: "api", DeploymentID: customerBaseline,
		From: "2026-10-01T00:00:00Z", Until: "2026-10-02T00:00:00Z", Coverage: "observed_only", RoutesLimit: 200, CustomersLimit: 20,
		Routes: []api.RouteCustomerUsage{{Route: "GET /users/{id}", Method: "GET", Requests: 19, IdentifiedRequests: 12, AnonymousRequests: 5, UnresolvedIdentityRequests: 2,
			ConsumerCount: 3, PlatformTenantCount: 1, LastObservedAt: "2026-10-01T23:00:00Z", CustomersTruncated: true, OtherCustomerRequests: 4,
			Customers: []api.RouteCustomerObservation{{ConsumerID: customerID, PlatformTenantID: customerTenant, Requests: 8, LastObservedAt: "2026-10-01T23:00:00Z"}},
		}},
	}
}

func customerReportFixture(t *testing.T) previewRouteReport {
	t.Helper()
	return previewRouteReport{Parent: "api", BaselineDeployment: customerBaseline,
		Routes: comparePreviewReportContracts(previewReportSpec(t, previewReportBefore), previewReportSpec(t, previewReportAfter))}
}

func TestPreviewCustomerImpactRedactsIdentitiesInJSONTextAndMarkdown(t *testing.T) {
	for _, details := range []bool{false, true} {
		report := customerReportFixture(t)
		data := customerUsageFixture()
		attachPreviewCustomerUsage(&report, data, details)
		if report.Customers.Status != "advisory" || report.Customers.DetailsIncluded != details {
			t.Fatalf("%+v", report.Customers)
		}
		row := findPreviewReportRoute(t, report, "GET /users/{id}").CustomerImpact
		if row == nil || row.Usage.ConsumerCount != 3 || row.Usage.AnonymousRequests != 5 || !row.Usage.CustomersTruncated || row.Usage.OtherCustomerRequests != 4 {
			t.Fatalf("%+v", row)
		}
		if len(data.Routes[0].Customers) != 1 {
			t.Fatal("redaction mutated API evidence")
		}
		prioritizePreviewRouteReview(&report)
		body, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		outputs := []string{string(body)}
		for _, markdown := range []bool{false, true} {
			var b bytes.Buffer
			renderPreviewRouteReport(&b, report, markdown)
			outputs = append(outputs, b.String())
		}
		for _, output := range outputs {
			for _, id := range []string{customerID, customerTenant} {
				if strings.Contains(output, id) != details {
					t.Fatalf("details=%t: identity exposure in %s", details, output)
				}
			}
		}
	}
}

func TestPreviewCustomerImpactDoesNotInferMissingRoutesOrUnboundEvidence(t *testing.T) {
	for _, truncated := range []bool{false, true} {
		report := customerReportFixture(t)
		data := customerUsageFixture()
		data.RoutesTruncated = truncated
		attachPreviewCustomerUsage(&report, data, false)
		want := "no_observations"
		if truncated {
			want = "not_in_bounded_inventory"
		}
		row := findPreviewReportRoute(t, report, "DELETE /old").CustomerImpact
		if row.Status != want || row.Usage != nil {
			t.Fatalf("missing route = %+v", row)
		}
	}
	for _, mutate := range []func(*api.RouteCustomerUsageResponse){
		func(r *api.RouteCustomerUsageResponse) { r.Slug = "different" },
		func(r *api.RouteCustomerUsageResponse) { r.DeploymentID = customerID },
		func(r *api.RouteCustomerUsageResponse) { r.Coverage = "complete" },
		func(r *api.RouteCustomerUsageResponse) { r.From = "invalid" },
		func(r *api.RouteCustomerUsageResponse) { r.Until = r.From },
	} {
		report := customerReportFixture(t)
		data := customerUsageFixture()
		mutate(&data)
		attachPreviewCustomerUsage(&report, data, true)
		if report.Customers.Status != "unavailable" || report.Routes[0].CustomerImpact != nil {
			t.Fatalf("unbound response attached: %+v", report)
		}
	}
	report := customerReportFixture(t)
	data := customerUsageFixture()
	data.Routes = append(data.Routes, data.Routes[0])
	attachPreviewCustomerUsage(&report, data, false)
	if row := findPreviewReportRoute(t, report, "GET /users/{id}").CustomerImpact; row.Status != "ambiguous_route" || row.Usage != nil {
		t.Fatalf("%+v", row)
	}
}

func TestPreviewReportCollectsCustomerUsageFromSelectedParentDeployment(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	var customerReads int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("mutating read: %s", r.Method)
		}
		switch r.URL.Path {
		case "/v1/preview/pr-42-api":
			writeJSONTest(w, api.PreviewResourceResponse{App: api.AppResponse{ID: "preview", Slug: "pr-42-api", PreviewOfSlug: "api"}, Parent: &api.AppResponse{ID: "parent", Slug: "api"}, LatestDeployment: &api.DeploymentResponse{ID: "candidate", AppID: "preview", Status: "live"}, ProductionDeployment: &api.DeploymentResponse{ID: customerBaseline, AppID: "parent", Status: "live"}})
		case "/v1/apps/api/deployments/" + customerBaseline + "/openapi":
			writePreviewReportDoc(t, w, customerBaseline, previewReportBefore)
		case "/v1/apps/pr-42-api/deployments/candidate/openapi":
			writePreviewReportDoc(t, w, "candidate", previewReportAfter)
		case "/v1/apps/api/deployments/" + customerBaseline + "/route-policy":
			writePreviewPolicySnapshotTest(w, customerBaseline, "parent", []api.EdgeRuleResponse{})
		case "/v1/apps/pr-42-api/deployments/candidate/route-policy":
			writePreviewPolicySnapshotTest(w, "candidate", "preview", []api.EdgeRuleResponse{})
		case "/v1/apps/pr-42-api/openapi/preview":
			writeJSONTest(w, api.AppOpenAPIPolicyPreviewResponse{})
		case "/v1/apps/api/analytics", "/v1/apps/pr-42-api/analytics":
			writeJSONTest(w, api.RequestAnalyticsResponse{})
		case "/v1/apps/api/analytics/route-customers":
			customerReads++
			if r.URL.Query().Get("deployment_id") != customerBaseline || r.URL.Query().Get("since") != "168h" {
				t.Errorf("scope %s", r.URL.RawQuery)
			}
			data := customerUsageFixture()
			data.Until = r.URL.Query().Get("until")
			if _, err := time.Parse(time.RFC3339Nano, data.Until); err != nil {
				t.Errorf("missing shared window end: %v", err)
			}
			writeJSONTest(w, data)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	var out bytes.Buffer
	old := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = old })
	jsonOutput = true
	if code := run([]string{"preview", "report", "pr-42-api", "--since", "168h", "--customer-details", "--json"}); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	var report previewRouteReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if customerReads != 1 || report.Version != 7 || report.Customers.Status != "advisory" || !strings.Contains(out.String(), customerID) {
		t.Fatalf("reads %d: %s", customerReads, out.String())
	}
}
