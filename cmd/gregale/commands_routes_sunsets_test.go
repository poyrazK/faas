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

const sunsetDoc = `{"openapi":"3.0.0","paths":{"/old":{"get":{"deprecated":true,"x-gregale-deprecated-at":"2026-10-01T00:00:00Z","x-gregale-sunset-at":"2026-10-08T00:00:00Z","x-gregale-successor":"https://example.com/new"}},"/later":{"post":{"deprecated":true,"x-gregale-deprecated-at":"2026-10-01T00:00:00Z","x-gregale-sunset-at":"2026-10-09T00:00:00Z","x-gregale-successor":"https://example.com/new"}},"/undated":{"get":{"deprecated":true}},"/healthy":{"get":{}}}}`

func TestRouteSunsetEvidence(t *testing.T) {
	spec, err := openapidiff.LoadBytes([]byte(sunsetDoc))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	usage := api.RouteCustomerUsageResponse{Slug: "api", DeploymentID: "old", Coverage: "observed_only", From: "2026-10-01T00:00:00Z", Until: now.Format(time.RFC3339), Routes: []api.RouteCustomerUsage{{Method: "GET", Route: "GET /old", Requests: 3, LastObservedAt: "2026-10-07T00:00:00Z", Customers: []api.RouteCustomerObservation{{ConsumerID: "c", PlatformTenantID: "t", Requests: 3}}}}}
	review := &routeMigrationReviewReport{Mappings: []routeMigrationRouteReview{{From: previewCustomerMigrationEndpoint{App: "api", Method: "GET", Path: "/old"}, Status: "no_supported_breaks", Successors: []routeMigrationPairReview{{To: previewCustomerMigrationEndpoint{App: "api", Method: "GET", Path: "/new"}, Status: "no_supported_breaks"}}}}}
	next := usage
	next.Routes = []api.RouteCustomerUsage{{Route: "/new", Method: "GET", Requests: 1, Customers: []api.RouteCustomerObservation{{ConsumerID: "c", PlatformTenantID: "t"}}}}
	report := buildRouteSunsetReport("api", "old", []byte(sunsetDoc), spec, usage, review, map[string]api.RouteCustomerUsageResponse{"api": next}, now, 48*time.Hour)
	if len(report.Routes) != 3 || report.Routes[0].Status != "overdue" || report.Routes[1].Status != "upcoming" || report.Routes[2].Status != "unscheduled" {
		t.Fatalf("rows %+v", report.Routes)
	}
	row := report.Routes[0]
	if row.Requests != 3 || row.LastObservedAt != usage.Routes[0].LastObservedAt || row.Successors[0].CallersAlsoObserved != 1 || row.Successors[0].Requests != 1 || row.ContractStatus != "no_supported_breaks" {
		t.Fatalf("evidence %+v", row)
	}
	usage.RoutesTruncated = true
	report = buildRouteSunsetReport("api", "old", nil, spec, usage, nil, nil, now, time.Hour)
	if report.Routes[0].Evidence != "incomplete" || report.Routes[0].ContractStatus != "not_reviewed" {
		t.Fatalf("missing evidence %+v", report.Routes[0])
	}
	usage.RoutesTruncated = false
	usage.Routes[0].AnonymousRequests = 1
	_, status := sunsetUsage(usage, "GET", "/old")
	if status != "incomplete" {
		t.Fatal("anonymous caller completeness")
	}
	usage.Routes[0].AnonymousRequests = 0
	usage.Routes[0].CustomersTruncated = true
	_, status = sunsetUsage(usage, "GET", "/old")
	if status != "incomplete" {
		t.Fatal("truncated caller completeness")
	}
}

func TestRouteSunsetCommand(t *testing.T) {
	resetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps/api/openapi":
			if r.URL.Query().Get("source") != "manual_import" {
				t.Error("wrong document source")
			}
			w.Write([]byte(sunsetDoc))
		case "/v1/apps/api/analytics/route-customers":
			if r.URL.Query().Get("deployment_id") != routeMigrationFromDeployment {
				t.Error("wrong deployment")
			}
			json.NewEncoder(w).Encode(api.RouteCustomerUsageResponse{Slug: "api", DeploymentID: routeMigrationFromDeployment, Coverage: "observed_only", From: "2026-10-01T00:00:00Z", Until: "2026-10-08T00:00:00Z"})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	var output bytes.Buffer
	old, oldJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &output, true
	t.Cleanup(func() { osStdout, jsonOutput = old, oldJSON })
	if code := cmdRoutes([]string{"sunsets", "api", "--source", "manual_import", "--deployment", routeMigrationFromDeployment, "--fail-on-incomplete"}); code != 1 {
		t.Fatalf("expected incomplete exit, got %d: %s", code, output.String())
	}
	var report routeSunsetReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Version != 1 || report.DeploymentID != routeMigrationFromDeployment || len(report.ImportedSHA256) != 64 || len(report.Routes) != 3 {
		t.Fatalf("report %+v", report)
	}
}

func TestRouteSunsetMappedCommand(t *testing.T) {
	resetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
	mappingPath := filepath.Join(t.TempDir(), "mapping.json")
	if err := os.WriteFile(mappingPath, []byte(`{"version":1,"mappings":[{"from":{"app":"api","method":"GET","path":"/old"},"successors":[{"app":"api","method":"GET","path":"/new"}]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	sourceBytes := []byte(`{"openapi":"3.0.0","paths":{"/old":{"get":{"responses":{"200":{"description":"OK"}}}}}}`)
	targetBytes := []byte(`{"openapi":"3.0.0","paths":{"/new":{"get":{"responses":{"200":{"description":"OK"}}}}}}`)
	var sourceDoc, targetDoc map[string]any
	if err := json.Unmarshal(sourceBytes, &sourceDoc); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(targetBytes, &targetDoc); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps/api/openapi":
			w.Write([]byte(sunsetDoc))
		case "/v1/deployments/" + routeMigrationFromDeployment:
			json.NewEncoder(w).Encode(api.DeploymentResponse{ID: routeMigrationFromDeployment, AppID: "app-id"})
		case "/v1/deployments/" + routeMigrationToDeployment:
			json.NewEncoder(w).Encode(api.DeploymentResponse{ID: routeMigrationToDeployment, AppID: "app-id"})
		case "/v1/apps/api/deployments/" + routeMigrationFromDeployment + "/openapi":
			json.NewEncoder(w).Encode(api.OpenAPIDocResponse{AppID: "app-id", DeploymentID: routeMigrationFromDeployment, Source: "manual_upload", CapturedAt: "2026-10-01T00:00:00Z", Doc: sourceDoc})
		case "/v1/apps/api/deployments/" + routeMigrationToDeployment + "/openapi":
			json.NewEncoder(w).Encode(api.OpenAPIDocResponse{AppID: "app-id", DeploymentID: routeMigrationToDeployment, Source: "manual_upload", CapturedAt: "2026-10-01T00:00:00Z", Doc: targetDoc})
		case "/v1/apps/api/analytics/route-customers":
			id := r.URL.Query().Get("deployment_id")
			route := "/old"
			if id == routeMigrationToDeployment {
				route = "/new"
				if r.URL.Query().Get("since") != "2026-10-01T00:00:00Z" || r.URL.Query().Get("until") != "2026-10-08T00:00:00Z" {
					t.Error("unbound successor window")
				}
			}
			json.NewEncoder(w).Encode(api.RouteCustomerUsageResponse{Slug: "api", DeploymentID: id, Coverage: "observed_only", From: "2026-10-01T00:00:00Z", Until: "2026-10-08T00:00:00Z", Routes: []api.RouteCustomerUsage{{Route: route, Method: "GET", Requests: 1, Customers: []api.RouteCustomerObservation{{ConsumerID: "c", Requests: 1}}}}})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	var output bytes.Buffer
	old, oldJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &output, true
	t.Cleanup(func() { osStdout, jsonOutput = old, oldJSON })
	if code := cmdRoutesSunsets([]string{"api", "--source", "manual_import", "--deployment", routeMigrationFromDeployment, "--mapping", mappingPath, "--to-deployment", "api=" + routeMigrationToDeployment}); code != 0 {
		t.Fatalf("exit %d: %s", code, output.String())
	}
	var report routeSunsetReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.ContractReview == nil || len(report.Routes[0].Successors) != 1 || report.Routes[0].Successors[0].CallersAlsoObserved != 1 || !report.Routes[0].Successors[0].IdentityComparable {
		t.Fatalf("mapping evidence %+v", report)
	}
}

func TestRouteSunsetDeploymentMetadata(t *testing.T) {
	resetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
	var document map[string]any
	if err := json.Unmarshal([]byte(sunsetDoc), &document); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/deployments/" + routeMigrationFromDeployment:
			json.NewEncoder(w).Encode(api.DeploymentResponse{ID: routeMigrationFromDeployment, AppID: "app"})
		case "/v1/apps/api/deployments/" + routeMigrationFromDeployment + "/openapi":
			json.NewEncoder(w).Encode(api.OpenAPIDocResponse{AppID: "app", DeploymentID: routeMigrationFromDeployment, Source: "manual_upload", DocSHA256: strings.Repeat("a", 64), CapturedAt: "2026-10-01T00:00:00Z", Doc: document})
		case "/v1/apps/api/analytics/route-customers":
			json.NewEncoder(w).Encode(api.RouteCustomerUsageResponse{Slug: "api", DeploymentID: routeMigrationFromDeployment, Coverage: "observed_only", From: "2026-10-01T00:00:00Z", Until: "2026-10-08T00:00:00Z"})
		default:
			t.Errorf("unexpected read %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	var output bytes.Buffer
	old, oldJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &output, true
	t.Cleanup(func() { osStdout, jsonOutput = old, oldJSON })
	if code := cmdRoutesSunsets([]string{"api", "--deployment", routeMigrationFromDeployment}); code != 0 {
		t.Fatalf("exit %d: %s", code, output.String())
	}
	var report routeSunsetReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.MetadataSource != "deployment" || report.CaptureSHA256 != strings.Repeat("a", 64) || report.CaptureSource != "manual_upload" || report.ImportedSHA256 != "" || len(report.Routes) != 3 {
		t.Fatalf("capture provenance %+v", report)
	}
}
