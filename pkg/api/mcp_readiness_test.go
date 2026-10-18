package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPReadinessPolicyValidation(t *testing.T) {
	valid := SetMCPReadinessPolicyRequest{Enabled: true, Endpoint: "/mcp", MaxAgeSeconds: 300, Reason: "Reviewed release", Roles: []MCPReadinessRole{{Name: "reader", ProtocolVersion: MCPProtocolVersion, CatalogFormat: 2, CatalogSHA256: strings.Repeat("a", 64)}}}
	if err := ValidateMCPReadinessPolicy(valid); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"https://foreign.example/mcp", "//foreign.example/mcp", "/mcp?token=secret", "/mcp#fragment", "/%6dcp"} {
		r := valid
		r.Endpoint = path
		if ValidateMCPReadinessPolicy(r) == nil {
			t.Fatalf("unsafe endpoint accepted: %s", path)
		}
	}
	r := valid
	r.MaxAgeSeconds = 3601
	if ValidateMCPReadinessPolicy(r) == nil {
		t.Fatal("unbounded freshness accepted")
	}
	r = valid
	r.Roles = append(r.Roles, r.Roles[0])
	if ValidateMCPReadinessPolicy(r) == nil {
		t.Fatal("duplicate roles accepted")
	}
}
func TestMCPReadinessClientTokenSeparation(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer PLATFORM_ONLY" {
			t.Error("platform authorization missing")
		}
		if r.URL.Path != "/v1/deployments/candidate/mcp-readiness" || r.Method != "POST" {
			t.Error("wrong endpoint")
		}
		if r.Header.Get(MCPReadinessTokenHeader) != "PROBE_ONLY" {
			t.Error("separate probe credential missing")
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body["role"] != "reader" {
			t.Fatal("client submitted non-role evidence")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"deployment_id":"candidate","role":"reader","passed":false}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "PLATFORM_ONLY")
	evidence, err := client.ProbeMCPReadiness(t.Context(), "candidate", "reader", "PROBE_ONLY")
	if err != nil || evidence.Passed || evidence.Role != "reader" || calls != 1 {
		t.Fatalf("probe result: %+v %v", evidence, err)
	}
	if _, err = client.ProbeMCPReadiness(t.Context(), "candidate", "reader", "bad token\n"); err == nil || calls != 1 {
		t.Fatal("invalid probe token was forwarded")
	}
}

func TestMCPReadinessMonitoringValidation(t *testing.T) {
	for _, c := range []MCPReadinessMonitorConfig{{Enabled: true, IntervalSeconds: 59, FailureThreshold: 2}, {Enabled: true, IntervalSeconds: 60, FailureThreshold: 0}, {Enabled: true, IntervalSeconds: 60, FailureThreshold: 2, SecretRef: "raw-token"}} {
		if ValidateMCPReadinessMonitor(c) == nil {
			t.Fatalf("accepted invalid monitor %+v", c)
		}
	}
	if err := ValidateMCPReadinessMonitor(MCPReadinessMonitorConfig{Enabled: true, IntervalSeconds: 60, FailureThreshold: 2, SecretRef: "secret:READINESS_TOKEN"}); err != nil {
		t.Fatal(err)
	}
}

func TestMCPReadinessOAuthReferenceValidation(t *testing.T) {
	valid := MCPReadinessMonitorConfig{Enabled: true, IntervalSeconds: 60, FailureThreshold: 2, OAuthConnection: &MCPReadinessOAuthConnection{Role: "reader", Revision: "7e53717e-9b82-4ea4-a594-4a1d8aa2a8bb"}}
	if err := ValidateMCPReadinessMonitor(valid); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"inline", "missing_role", "bad_revision"} {
		c := valid
		ref := *valid.OAuthConnection
		c.OAuthConnection = &ref
		switch mode {
		case "inline":
			c.SecretRef = "secret:TOKEN"
		case "missing_role":
			ref.Role = ""
		case "bad_revision":
			ref.Revision = "stale"
		}
		if ValidateMCPReadinessMonitor(c) == nil {
			t.Fatal("accepted invalid OAuth binding", mode)
		}
	}
}
