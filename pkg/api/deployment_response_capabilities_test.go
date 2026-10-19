package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeploymentResponseCheckCapabilityClientIsReadOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/deployments/response-check/capabilities" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer owner-token" {
			t.Error("SDK capability request changed its route or authentication")
		}
		_ = json.NewEncoder(w).Encode(DeploymentResponseCheckCapabilities{ProtocolVersion: DeploymentResponseCheckProtocolVersion, Available: true})
	}))
	defer server.Close()
	result, err := NewClient(server.URL, "owner-token").GetDeploymentResponseCheckCapabilities(t.Context())
	if err != nil || !result.SupportsCurrentProtocol() {
		t.Fatalf("capability request failed: result=%+v err=%v", result, err)
	}
}

func TestDeploymentResponseCheckCapabilityDoesNotFollowRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/deployments/response-check/capabilities" {
			t.Error("compatibility read followed a redirect to another route")
			return
		}
		http.Redirect(w, r, "/must-not-contact", http.StatusFound)
	}))
	defer server.Close()
	if result, err := NewClient(server.URL, "owner-token").GetDeploymentResponseCheckCapabilities(t.Context()); err == nil || result.SupportsCurrentProtocol() {
		t.Fatal("redirected endpoint established compatibility")
	}
}
