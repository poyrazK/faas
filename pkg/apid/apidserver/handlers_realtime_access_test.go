package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRealtimeAccessPreviewRequiresOIDCAndPreservesSessionCutoff(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	endpoint := createRealtimeEndpointForTest(t, e)
	path := "/v1/apps/rt-actions/realtime/endpoints/" + endpoint + "/principals/user-123/access"
	request := api.ManagedRealtimeAccessRequest{Action: "revoke_channel", Channel: "orders"}
	if response := e.Do(t, http.MethodPost, path, request, nil); response.Code != http.StatusNotFound {
		t.Fatalf("disabled preview: %d %s", response.Code, response.Body)
	}
	(*e.S()).WithRealtimeAccessPreviewEnabled(true)
	if response := e.Do(t, http.MethodPost, path, request, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("non-OIDC: %d %s", response.Code, response.Body)
	}
	mode := "oidc_jwt"
	if _, err := (*e.Store()).UpdateManagedRealtimeEndpoint(t.Context(), endpoint, state.UpdateManagedRealtimeEndpointParams{AuthMode: &mode}); err != nil {
		t.Fatal(err)
	}
	response := e.Do(t, http.MethodPost, path, request, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("deny: %d %s", response.Code, response.Body)
	}
	var access state.ManagedRealtimeAccess
	if err := json.Unmarshal(response.Body.Bytes(), &access); err != nil || len(access.DeniedChannels) != 1 {
		t.Fatalf("deny response: %+v %v", access, err)
	}
	request = api.ManagedRealtimeAccessRequest{Action: "revoke_sessions"}
	response = e.Do(t, http.MethodPost, path, request, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("session cutoff: %d %s", response.Code, response.Body)
	}
	if err := json.Unmarshal(response.Body.Bytes(), &access); err != nil || access.SessionsRevokedBefore == nil {
		t.Fatalf("cutoff response: %+v %v", access, err)
	}
	response = e.Do(t, http.MethodGet, path, nil, nil)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("access read: %d %s", response.Code, response.Body)
	}
	request.Channel = "orders"
	if response = e.Do(t, http.MethodPost, path, request, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("session action with channel: %d %s", response.Code, response.Body)
	}
}
