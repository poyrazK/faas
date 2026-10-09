package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
)

func migrationSuggestionDeployment(t *testing.T, paths map[string]any) routeMigrationLoadedDeployment {
	t.Helper()
	body, err := json.Marshal(map[string]any{"openapi": "3.1.0", "paths": paths})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := openapidiff.LoadBytes(body)
	if err != nil {
		t.Fatal(err)
	}
	return routeMigrationLoadedDeployment{evidence: routeMigrationDeploymentEvidence{Status: "available"}, spec: spec}
}

func migrationSuggestionPath(path, id string) any {
	document := routeMigrationContractDocument(path, "name")
	item := document["paths"].(map[string]any)[path].(map[string]any)
	item["get"].(map[string]any)["operationId"] = id
	return item
}

func TestRoutesMigrationSuggestionsRankPathsAndExcludeUnrelatedSchemas(t *testing.T) {
	from := map[string]routeMigrationLoadedDeployment{"api": migrationSuggestionDeployment(t, map[string]any{"/v1/users/{id}": migrationSuggestionPath("/v1/users/{id}", ""), "/same/{id}": migrationSuggestionPath("/same/{id}", "")})}
	to := map[string]routeMigrationLoadedDeployment{"api": migrationSuggestionDeployment(t, map[string]any{"/v2/users/{userId}": migrationSuggestionPath("/v2/users/{userId}", ""), "/same/{id}": migrationSuggestionPath("/same/{id}", ""), "/invoices/{id}": migrationSuggestionPath("/invoices/{id}", "")})}
	report, err := buildRouteMigrationSuggestions(from, to, nil, nil, nil, 3, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Sources) != 1 || report.Sources[0].Status != "suggested" || len(report.Sources[0].Candidates) != 1 || report.Draft.Mappings[0].Successors[0].Path != "/v2/users/{userId}" {
		t.Fatalf("expected one clear versioned successor and unchanged route omitted: %+v", report)
	}
	if !report.OwnerReviewRequired {
		t.Fatal("suggestions must require owner review")
	}
}

func TestRoutesMigrationSuggestionsKeepAmbiguityDespiteTrafficAndLimit(t *testing.T) {
	from := map[string]routeMigrationLoadedDeployment{"api": migrationSuggestionDeployment(t, map[string]any{"/v1/users/{id}": migrationSuggestionPath("/v1/users/{id}", "")})}
	to := map[string]routeMigrationLoadedDeployment{"api": migrationSuggestionDeployment(t, map[string]any{"/v2/users/{id}": migrationSuggestionPath("/v2/users/{id}", ""), "/v3/users/{id}": migrationSuggestionPath("/v3/users/{id}", "")})}
	usage := indexPreviewCustomerMigrationUsage(previewCustomerMigrationAppReport{Status: "available"}, api.RouteCustomerUsageResponse{Routes: []api.RouteCustomerUsage{{Method: "GET", Route: "GET /v3/users/{id}", Requests: 99}}})
	report, err := buildRouteMigrationSuggestions(from, to, nil, map[string]previewCustomerMigrationAppEvidence{"api": usage}, nil, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	row := report.Sources[0]
	if row.Status != "ambiguous" || len(report.Draft.Mappings[0].Successors) != 0 || !row.CandidatesTruncated || row.CandidatesTotal != 2 || row.Candidates[0].To.Path != "/v3/users/{id}" {
		t.Fatalf("traffic/limit must not turn a tie into an accepted mapping: %+v", report)
	}
}

func TestRoutesMigrationSuggestionsUseOperationIDsAndHoldUnsupportedChanges(t *testing.T) {
	old := migrationSuggestionPath("/users/{id}", "getAccount").(map[string]any)
	next := migrationSuggestionPath("/accounts/{id}", "getAccount").(map[string]any)
	from := map[string]routeMigrationLoadedDeployment{"api": migrationSuggestionDeployment(t, map[string]any{"/users/{id}": old})}
	to := map[string]routeMigrationLoadedDeployment{"api": migrationSuggestionDeployment(t, map[string]any{"/accounts/{id}": next})}
	report, err := buildRouteMigrationSuggestions(from, to, nil, nil, nil, 3, time.Now())
	if err != nil || report.Sources[0].Status != "suggested" {
		t.Fatalf("operation ID should identify renamed route: %+v %v", report, err)
	}
	schema := next["get"].(map[string]any)["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	schema["additionalProperties"] = false
	to["api"] = migrationSuggestionDeployment(t, map[string]any{"/accounts/{id}": next})
	report, err = buildRouteMigrationSuggestions(from, to, nil, nil, nil, 3, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if report.Sources[0].Status != "contract_review_required" || report.Sources[0].Candidates[0].ContractStatus != "unknown" || len(report.Draft.Mappings[0].Successors) != 0 {
		t.Fatalf("unsupported changes should hold mapping: %+v", report)
	}
	to["unavailable"] = routeMigrationLoadedDeployment{}
	report, err = buildRouteMigrationSuggestions(from, to, nil, nil, nil, 3, time.Now())
	if err != nil || report.Status != "incomplete" || report.Sources[0].Status != "candidate_inventory_incomplete" {
		t.Fatalf("missing inventory must prevent selection: %+v %v", report, err)
	}
}

func TestRoutesMigrationSuggestionsDoNotInferReplacementFromGenericShape(t *testing.T) {
	from := migrationSuggestionDeployment(t, map[string]any{"/v1/{id}": migrationSuggestionPath("/v1/{id}", "")})
	to := migrationSuggestionDeployment(t, map[string]any{"/v2/{other}": migrationSuggestionPath("/v2/{other}", "")})
	score, _ := routeMigrationSimilarity(from.spec, previewCustomerMigrationEndpoint{Method: "GET", Path: "/v1/{id}"}, to.spec, previewCustomerMigrationEndpoint{Method: "GET", Path: "/v2/{other}"})
	if score != 0 {
		t.Fatalf("generic parameter shape is not route identity: score %d", score)
	}
	if got := strings.Join(routeMigrationPathShape("/users/v2/orders"), "/"); got != "users/v2/orders" {
		t.Fatalf("resource version segment must survive: %s", got)
	}
}

func TestRoutesMigrationSuggestionTrafficDoesNotClaimTruncatedAbsence(t *testing.T) {
	endpoint := previewCustomerMigrationEndpoint{App: "api", Method: "GET", Path: "/old"}
	usage := indexPreviewCustomerMigrationUsage(previewCustomerMigrationAppReport{Status: "incomplete"}, api.RouteCustomerUsageResponse{Routes: []api.RouteCustomerUsage{}})
	result := routeMigrationTraffic(endpoint, map[string]previewCustomerMigrationAppEvidence{"api": usage})
	if result.Status != "unknown" {
		t.Fatalf("truncated absence should remain unknown: %+v", result)
	}
	usage.report.Status = "available"
	result = routeMigrationTraffic(endpoint, map[string]previewCustomerMigrationAppEvidence{"api": usage})
	if result.Status != "not_observed" {
		t.Fatalf("complete empty observation: %+v", result)
	}
}

func TestRoutesMigrationSuggestReadsBoundContractsAndWritesReviewableMapping(t *testing.T) {
	const customerID = "44444444-4444-4444-8444-444444444444"
	resetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		if r.Method != "GET" {
			t.Errorf("unexpected mutation %s", r.Method)
		}
		switch r.URL.Path {
		case "/v1/deployments/" + routeMigrationFromDeployment:
			writeJSONTest(w, api.DeploymentResponse{ID: routeMigrationFromDeployment, AppID: "checkout-id"})
		case "/v1/deployments/" + routeMigrationToDeployment:
			writeJSONTest(w, api.DeploymentResponse{ID: routeMigrationToDeployment, AppID: "checkout-id"})
		case "/v1/apps/checkout/deployments/" + routeMigrationFromDeployment + "/openapi":
			writeJSONTest(w, api.OpenAPIDocResponse{DeploymentID: routeMigrationFromDeployment, AppID: "checkout-id", Source: "manual_upload", CapturedAt: "2026-10-01T00:00:00Z", Doc: routeMigrationContractDocument("/v1/users/{id}", "name")})
		case "/v1/apps/checkout/deployments/" + routeMigrationToDeployment + "/openapi":
			writeJSONTest(w, api.OpenAPIDocResponse{DeploymentID: routeMigrationToDeployment, AppID: "checkout-id", Source: "manual_upload", CapturedAt: "2026-10-02T00:00:00Z", Doc: routeMigrationContractDocument("/v2/users/{id}", "name")})
		case "/v1/apps/checkout/analytics/route-customers":
			id := r.URL.Query().Get("deployment_id")
			if id != routeMigrationFromDeployment && id != routeMigrationToDeployment || r.URL.Query().Get("since") != "14d" {
				t.Errorf("unbound traffic request: %s", r.URL.String())
			}
			until, err := time.Parse(time.RFC3339Nano, r.URL.Query().Get("until"))
			if err != nil {
				t.Errorf("missing shared until: %v", err)
			}
			path := "/v1/users/{id}"
			if id == routeMigrationToDeployment {
				path = "/v2/users/{id}"
			}
			last := until.Add(-time.Minute).Format(time.RFC3339Nano)
			writeJSONTest(w, api.RouteCustomerUsageResponse{Slug: "checkout", DeploymentID: id,
				From: until.Add(-14 * 24 * time.Hour).Format(time.RFC3339Nano), Until: until.Format(time.RFC3339Nano), AsOf: until.Add(time.Minute).Format(time.RFC3339Nano), Coverage: "observed_only",
				RoutesLimit: api.RouteCustomerUsageMaxRoutes, CustomersLimit: api.RouteCustomerUsageMaxCustomers,
				Routes: []api.RouteCustomerUsage{{Method: "GET", Route: "GET " + path, Requests: 10, IdentifiedRequests: 10, ConsumerCount: 1, PlatformTenantCount: 1, LastObservedAt: last,
					Customers: []api.RouteCustomerObservation{{ConsumerID: customerID, PlatformTenantID: customerTenant, Requests: 10, LastObservedAt: last}}}}})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	var output bytes.Buffer
	oldStdout, oldJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &output, true
	t.Cleanup(func() { osStdout, jsonOutput = oldStdout, oldJSON })
	mappingPath := filepath.Join(t.TempDir(), "mapping.json")
	reportPath := filepath.Join(t.TempDir(), "report.json")
	args := []string{"suggest", "--from-deployment", "checkout=" + routeMigrationFromDeployment, "--to-deployment", "checkout=" + routeMigrationToDeployment, "--out", mappingPath, "--report-out", reportPath}
	if code := cmdRoutesMigration(args); code != 0 {
		t.Fatalf("suggest exit %d: %s", code, output.String())
	}
	if reads != 6 {
		t.Fatalf("expected bound captures and traffic, got %d reads", reads)
	}
	mappings, err := readPreviewCustomerMigrationMappings(mappingPath)
	if err != nil || len(mappings) != 1 {
		t.Fatalf("draft must roundtrip into review: %+v %v", mappings, err)
	}
	for _, mapping := range mappings {
		if len(mapping.Successors) != 1 || mapping.Successors[0].Path != "/v2/users/{id}" {
			t.Fatalf("draft %+v", mapping)
		}
	}
	var report routeMigrationSuggestionReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.FromDeployments[0].ContractSHA) != 64 || report.FromDeployments[0].ContractSource != "manual_upload" {
		t.Fatalf("missing bound provenance: %+v", report)
	}
	if report.Sources[0].Traffic.Requests != 10 || len(report.FromObservation) != 1 || strings.Contains(output.String(), customerID) || strings.Contains(output.String(), customerTenant) {
		t.Fatalf("traffic evidence must be bound and redact identities by default: %s", output.String())
	}
	before, _ := os.ReadFile(mappingPath)
	if code := cmdRoutesMigration(args); code != 1 {
		t.Fatal("must not overwrite existing draft")
	}
	after, _ := os.ReadFile(mappingPath)
	if !bytes.Equal(before, after) || reads != 6 {
		t.Fatal("overwrite preflight must preserve files and avoid reads")
	}
	output.Reset()
	if code := cmdRoutesMigration(append(args[:5:5], "--customer-details")); code != 0 {
		t.Fatalf("explicit customer details exit %d: %s", code, output.String())
	}
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.CustomerDetailsIncluded || len(report.Sources[0].ObservedCustomers) != 1 || report.Sources[0].ObservedCustomers[0].ConsumerID != customerID {
		t.Fatalf("explicit details should identify observed source customers: %+v", report.Sources)
	}
}
