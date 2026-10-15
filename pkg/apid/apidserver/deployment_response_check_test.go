package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDeploymentResponseCheckAuthenticatedRoute(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "private-check")
	dep, err := (*e.Store()).CreateDeployment(t.Context(), state.Deployment{AppID: appID, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "" {
			t.Error("account credentials reached gateway forwarding")
		}
		_ = json.NewEncoder(w).Encode(api.DeploymentResponseCheckResult{AppID: appID, DeploymentID: dep.ID, Scope: "default", InstanceID: uuid.NewString(), HTTPStatus: 200, Body: "ready"})
	}))
	defer gateway.Close()
	(*e.S()).WithGatewaydControlURL(gateway.URL)
	path := "/v1/apps/private-check/deployments/" + dep.ID + "/response-check"
	request := api.DeploymentResponseCheckRequest{AppID: appID, Scope: "default", Path: "/ready"}
	if response := e.Do(t, http.MethodPost, path, request, map[string]string{"Authorization": "Bearer invalid"}); response.Code != http.StatusUnauthorized || calls != 0 {
		t.Fatalf("unauthenticated check reached gateway: %d %s", response.Code, response.Body)
	}
	response := e.Do(t, http.MethodPost, path, request, nil)
	if response.Code != http.StatusOK || calls != 1 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("authenticated check failed: %d %s", response.Code, response.Body)
	}
}
