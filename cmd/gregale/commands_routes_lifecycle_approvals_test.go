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
)

func TestRoutesLifecycleApprovalWorkflow(t *testing.T) {
	resetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
	receiptID := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	var submitted api.ApproveRouteLifecycleRequest
	receipt := api.RouteLifecycleApproval{ID: receiptID, AppID: "app", Compatibility: "no_supported_breaks", ApprovedBy: "server-owner", ValidUntil: time.Now().Add(time.Hour)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/deployments/" + routeMigrationFromDeployment, "/v1/deployments/" + routeMigrationToDeployment:
			id := strings.TrimPrefix(r.URL.Path, "/v1/deployments/")
			json.NewEncoder(w).Encode(api.DeploymentResponse{ID: id, AppID: "app"})
		case "/v1/apps/api/deployments/" + routeMigrationFromDeployment + "/openapi", "/v1/apps/api/deployments/" + routeMigrationToDeployment + "/openapi":
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/apps/api/deployments/"), "/openapi")
			json.NewEncoder(w).Encode(api.OpenAPIDocResponse{DeploymentID: id, AppID: "app", Source: "manual_upload", DocSHA256: strings.Repeat("a", 64), Doc: map[string]any{"openapi": "3.1.0", "paths": map[string]any{"/old": map[string]any{"get": map[string]any{}}}}})
		case "/v1/apps/api/route-requirements/gate":
			json.NewEncoder(w).Encode(api.CanaryRouteGate{AppID: "app", Mode: "enforce", Revision: 2})
		case "/v1/apps/api/route-removal/policy":
			json.NewEncoder(w).Encode(api.RouteRemovalPolicy{AppID: "app", Revision: 3})
		case "/v1/apps/api/route-requirements/check":
			json.NewEncoder(w).Encode(api.RouteRequirementsCheck{AppID: "app", DeploymentID: routeMigrationToDeployment, RequirementsRevision: 4, ConfigurationSHA256: strings.Repeat("b", 64)})
		case "/v1/apps/api/route-lifecycle/approvals":
			if r.Method != "POST" {
				t.Error("approval method", r.Method)
			}
			if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
				t.Error(err)
			}
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(receipt)
		case "/v1/apps/api/route-lifecycle/approvals/" + receiptID:
			json.NewEncoder(w).Encode(receipt)
		default:
			t.Error("unexpected", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	var output bytes.Buffer
	old, oldJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &output, true
	t.Cleanup(func() { osStdout, jsonOutput = old, oldJSON })
	mappings := filepath.Join(t.TempDir(), "mappings.json")
	requestPath := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(mappings, []byte(`[{"method":"GET","path":"/old","successor_url":"https://api.gregale.dev/new","successor_method":"GET","successor_path":"/new","successor_app_id":"dddddddd-dddd-4ddd-8ddd-dddddddddddd","successor_deployment_id":"eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee","successor_contract_sha256":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}]`), 0600); err != nil {
		t.Fatal(err)
	}
	code := cmdRoutes([]string{"lifecycle", "prepare-approval", "api", "--from-deployment", routeMigrationFromDeployment, "--to-deployment", routeMigrationToDeployment, "--mappings", mappings, "--out", requestPath})
	if code != 0 {
		t.Fatalf("prepare %d %s", code, output.String())
	}
	raw, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	var prepared api.ApproveRouteLifecycleRequest
	if err = json.Unmarshal(raw, &prepared); err != nil {
		t.Fatal(err)
	}
	if prepared.BaselineContractSHA256 != strings.Repeat("a", 64) || *prepared.ExpectedGateRevision != 2 || *prepared.ExpectedRequirementsRevision != 4 || *prepared.ExpectedRemovalPolicyRevision != 3 || prepared.ConfigurationSHA256 != strings.Repeat("b", 64) {
		t.Fatalf("request not server-pinned %+v", prepared)
	}
	output.Reset()
	if code := cmdRoutes([]string{"lifecycle", "approve", "api", "--request", requestPath}); code != 0 {
		t.Fatal("approve", code, output.String())
	}
	if submitted.CandidateDeploymentID != routeMigrationToDeployment || submitted.Mappings[0].SuccessorPath != "/new" || submitted.Mappings[0].SuccessorAppID != "dddddddd-dddd-4ddd-8ddd-dddddddddddd" || submitted.Mappings[0].SuccessorContractSHA256 != strings.Repeat("c", 64) {
		t.Fatalf("submitted %+v", submitted)
	}
	output.Reset()
	if code := cmdRoutes([]string{"lifecycle", "receipt", "api", "--id", receiptID}); code != 0 || !strings.Contains(output.String(), "server-owner") {
		t.Fatal("read", code, output.String())
	}
	forged := filepath.Join(t.TempDir(), "forged.json")
	if err = os.WriteFile(forged, []byte(`{"approved_by":"local-claim"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if code := cmdRoutes([]string{"lifecycle", "approve", "api", "--request", forged}); code == 0 {
		t.Fatal("local approval claim accepted")
	}
}
