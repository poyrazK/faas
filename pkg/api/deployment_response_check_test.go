package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestDeploymentResponseCheckContract(t *testing.T) {
	request := DeploymentResponseCheckRequest{AppID: strings.ReplaceAll(uuid.NewString(), "-", ""), Scope: "default", Path: "/ready?temporary=1"}
	for _, path := range []string{"", "//other.test/", "https://other.test/", "/ready#fragment", "/ready\n", "/\\other.test", "/" + strings.Repeat("a", DeploymentResponseCheckMaxPathBytes)} {
		invalid := request
		invalid.Path = path
		if invalid.Validate() == nil {
			t.Errorf("accepted invalid path %q", path)
		}
	}
	for _, scope := range []string{"", "__all__", "staging"} {
		invalid := request
		invalid.Scope = scope
		if invalid.Validate() == nil {
			t.Errorf("accepted unpinned scope %q", scope)
		}
	}
	request.Scope, request.ProjectID, request.EnvironmentID = "staging", uuid.NewString(), uuid.NewString()
	deploymentID := strings.ReplaceAll(uuid.NewString(), "-", "")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/v1/apps/check-api/deployments/"+deploymentID+"/response-check" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("SDK changed the authenticated target")
		}
		var received DeploymentResponseCheckRequest
		if json.NewDecoder(r.Body).Decode(&received) != nil || received != request {
			t.Errorf("SDK changed the request: %+v", received)
		}
		_ = json.NewEncoder(w).Encode(DeploymentResponseCheckResult{AppID: request.AppID, DeploymentID: deploymentID, Scope: request.Scope, ProjectID: request.ProjectID, EnvironmentID: request.EnvironmentID, InstanceID: uuid.NewString(), HTTPStatus: 401, Body: "Application auth required"})
	}))
	defer server.Close()
	client := NewClient(server.URL, "test-token")
	result, err := client.CheckDeploymentResponse(context.Background(), "check-api", deploymentID, request)
	if err != nil || !result.Matches(request, deploymentID) || result.HTTPStatus != 401 || calls != 1 {
		t.Fatalf("result=%+v calls=%d err=%v", result, calls, err)
	}
	if _, err := client.CheckDeploymentResponse(context.Background(), "check-api", "v3", request); err == nil || calls != 1 {
		t.Fatal("SDK sent an unpinned revision alias")
	}
}
