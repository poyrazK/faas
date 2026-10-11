package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routelifecycle"
)

func TestRoutesLifecycleDeclarationsCapturedContracts(t *testing.T) {
	resetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
	var before, after map[string]any
	if err := json.Unmarshal([]byte(`{"openapi":"3.1.0","paths":{"/health":{"get":{"deprecated":true,"x-gregale-deprecated-at":"2026-10-01T00:00:00Z","x-gregale-sunset-at":"2099-01-01T00:00:00Z","x-gregale-successor":"https://example.com/new"}}}}`), &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"openapi":"3.1.0","paths":{"/health":{"get":{}}}}`), &after); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/deployments/" + routeMigrationFromDeployment:
			json.NewEncoder(w).Encode(api.DeploymentResponse{ID: routeMigrationFromDeployment, AppID: "app"})
		case "/v1/deployments/" + routeMigrationToDeployment:
			json.NewEncoder(w).Encode(api.DeploymentResponse{ID: routeMigrationToDeployment, AppID: "app"})
		case "/v1/apps/api/deployments/" + routeMigrationFromDeployment + "/openapi":
			json.NewEncoder(w).Encode(api.OpenAPIDocResponse{DeploymentID: routeMigrationFromDeployment, AppID: "app", Source: "manual_upload", Doc: before})
		case "/v1/apps/api/deployments/" + routeMigrationToDeployment + "/openapi":
			json.NewEncoder(w).Encode(api.OpenAPIDocResponse{DeploymentID: routeMigrationToDeployment, AppID: "app", Source: "manual_upload", Doc: after})
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
	saved := filepath.Join(t.TempDir(), "review.json")
	code := cmdRoutes([]string{"lifecycle", "declarations", "api", "--from-deployment", routeMigrationFromDeployment, "--to-deployment", routeMigrationToDeployment, "--out", saved, "--fail-on-findings"})
	if code != 1 {
		t.Fatalf("expected finding exit, got %d: %s", code, output.String())
	}
	var report struct {
		Review routelifecycle.Review `json:"review"`
	}
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Review.Outcome != "regressions" || len(report.Review.BaselineSHA256) != 64 {
		t.Fatalf("review %+v", report.Review)
	}
	found := false
	for _, f := range report.Review.Findings {
		if f.Code == "deprecation_removed" {
			found = true
		}
	}
	if !found {
		t.Fatal("deprecation metadata was lost")
	}
	// Removing the final operation still compares against the protected baseline.
	after = map[string]any{"openapi": "3.1.0", "paths": map[string]any{}}
	output.Reset()
	jsonOutput = true
	code = cmdRoutes([]string{"lifecycle", "declarations", "api", "--from-deployment", routeMigrationFromDeployment, "--to-deployment", routeMigrationToDeployment, "--fail-on-findings"})
	if code != 1 {
		t.Fatalf("empty candidate exit %d", code)
	}
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Review.Findings) != 1 || report.Review.Findings[0].Code != "operation_removed_before_sunset" {
		t.Fatalf("empty candidate review %+v", report.Review)
	}

}
