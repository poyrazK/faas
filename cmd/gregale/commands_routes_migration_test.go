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

	"github.com/onebox-faas/faas/pkg/api"
)

const routeMigrationFromDeployment = "11111111-1111-4111-8111-111111111111"
const routeMigrationToDeployment = "22222222-2222-4222-8222-222222222222"

func TestRoutesMigrationReviewUsesBoundCapturedDeploymentContracts(t *testing.T) {
	resetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
	mappingFile := filepath.Join(t.TempDir(), "mapping.json")
	mapping := `{"version":1,"mappings":[{"from":{"app":"checkout","method":"GET","path":"/v1/users/{id}"},"successors":[{"app":"checkout","method":"GET","path":"/v2/accounts/{accountId}"}]}]}`
	if err := os.WriteFile(mappingFile, []byte(mapping), 0o600); err != nil {
		t.Fatal(err)
	}
	oldDocument := routeMigrationContractDocument("/v1/users/{id}", "name")
	newDocument := routeMigrationContractDocument("/v2/accounts/{accountId}", "")
	var reads int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("unexpected request: %s %s auth=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/v1/deployments/" + routeMigrationFromDeployment:
			writeJSONTest(w, api.DeploymentResponse{ID: routeMigrationFromDeployment, AppID: "checkout-id"})
		case "/v1/deployments/" + routeMigrationToDeployment:
			writeJSONTest(w, api.DeploymentResponse{ID: routeMigrationToDeployment, AppID: "checkout-id"})
		case "/v1/apps/checkout/deployments/" + routeMigrationFromDeployment + "/openapi":
			writeJSONTest(w, api.OpenAPIDocResponse{DeploymentID: routeMigrationFromDeployment, AppID: "checkout-id", Source: "manual_upload", CapturedAt: "2026-10-01T00:00:00Z", Doc: oldDocument})
		case "/v1/apps/checkout/deployments/" + routeMigrationToDeployment + "/openapi":
			writeJSONTest(w, api.OpenAPIDocResponse{DeploymentID: routeMigrationToDeployment, AppID: "checkout-id", Source: "manual_upload", CapturedAt: "2026-10-02T00:00:00Z", Doc: newDocument})
		default:
			t.Errorf("unexpected read: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)

	var output bytes.Buffer
	oldStdout, oldJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &output, true
	t.Cleanup(func() { osStdout, jsonOutput = oldStdout, oldJSON })
	args := []string{"review", "--mapping", mappingFile,
		"--from-deployment", "checkout=" + routeMigrationFromDeployment,
		"--to-deployment", "checkout=" + routeMigrationToDeployment,
	}
	if code := cmdRoutesMigration(args); code != 0 {
		t.Fatalf("migration review exit=%d: %s", code, output.String())
	}
	if reads != 4 {
		t.Fatalf("deployment contract reads=%d, want baseline and successor identity+contract reads", reads)
	}
	var report routeMigrationReviewReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatalf("decode review: %v (%s)", err, output.String())
	}
	if report.Outcome != "breaking_changes" || report.Summary.BreakingPairs != 1 || len(report.Mappings) != 1 ||
		report.Mappings[0].Successors[0].Status != "breaking" || !routeMigrationReviewHasCode(report, "response_field_removed") {
		t.Fatalf("captured contract difference was missed: %+v", report)
	}
	if len(report.FromDeployments) != 1 || report.FromDeployments[0].ContractSource != "manual_upload" || len(report.FromDeployments[0].ContractSHA) != 64 {
		t.Fatalf("review omitted the source or digest for the compared contract: %+v", report.FromDeployments)
	}

	output.Reset()
	args = append(args, "--fail-on-breaking")
	if code := cmdRoutesMigration(args); code != 1 {
		t.Fatalf("--fail-on-breaking exit=%d, want 1: %s", code, output.String())
	}
	if !strings.Contains(output.String(), `"outcome": "breaking_changes"`) {
		t.Fatalf("failing report was not emitted for review: %s", output.String())
	}
}

func routeMigrationContractDocument(path, responseField string) map[string]any {
	properties := map[string]any{"id": map[string]any{"type": "string"}}
	if responseField != "" {
		properties[responseField] = map[string]any{"type": "string"}
	}
	return map[string]any{
		"openapi": "3.1.0",
		"paths": map[string]any{
			path: map[string]any{
				"parameters": []any{map[string]any{"name": routeMigrationPathParameter(path), "in": "path", "required": true, "schema": map[string]any{"type": "string"}}},
				"get": map[string]any{
					"responses": map[string]any{
						"200": map[string]any{"description": "ok", "content": map[string]any{
							"application/json": map[string]any{"schema": map[string]any{"type": "object", "properties": properties, "required": []any{"id"}}},
						}},
					},
				},
			},
		},
	}
}

func routeMigrationPathParameter(path string) string {
	start := strings.IndexByte(path, '{')
	end := strings.IndexByte(path, '}')
	if start < 0 || end <= start {
		return ""
	}
	return path[start+1 : end]
}

func routeMigrationReviewHasCode(report routeMigrationReviewReport, code string) bool {
	for _, mapping := range report.Mappings {
		for _, successor := range mapping.Successors {
			for _, finding := range successor.Findings {
				if finding.Code == code {
					return true
				}
			}
		}
	}
	return false
}
