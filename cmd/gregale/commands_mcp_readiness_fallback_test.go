package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPReadinessFallbackCLI(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"version":1,"enabled":true}`, http.StatusOK)
	id := "00000000-0000-0000-0000-000000000001"
	base := []string{"--app", "demo", "--rule", id}
	if code := cmdMCPReadiness(append([]string{"fallback", "get"}, base...)); code != 0 || f.sawPath != "/v1/apps/demo/alerts/"+id+"/mcp-readiness-fallback" {
		t.Fatal(code, f.sawPath)
	}
	t.Setenv("MCP_FALLBACK_SIGNING_SECRET", "private-signing-material")
	args := append(append([]string{"set"}, base...), "--expected-version", "0", "--webhook-url", "https://fallback.example.com/hook", "--secret-env", "MCP_FALLBACK_SIGNING_SECRET", "--after-failures", "2")
	if code := cmdMCPReadinessFallback(args); code != 0 || f.sawMethod != "PUT" {
		t.Fatal(code)
	}
	var req api.SetMCPReadinessFallbackRequest
	if err := json.Unmarshal(f.sawBody, &req); err != nil || req.ExpectedVersion == nil || *req.ExpectedVersion != 0 || req.Enabled == nil || !*req.Enabled || req.WebhookSecret != "private-signing-material" || req.AfterFailures != 2 {
		t.Fatal(req, err)
	}
	if code := cmdMCPReadinessFallback(append(append([]string{"disable"}, base...), "--expected-version", "1")); code != 0 {
		t.Fatal(code)
	}
	for _, suffix := range [][]string{{"get", "--expected-version", "0"}, {"disable"}, {"set", "--expected-version", "0", "--webhook-url", "https://fallback.example.com", "--secret-env", "MISSING"}, {"disable", "--expected-version", "0", "--secret-env", "MCP_FALLBACK_SIGNING_SECRET"}} {
		f.sawMethod = ""
		if code := cmdMCPReadinessFallback(append(suffix, base...)); code == 0 || f.sawMethod != "" {
			t.Fatal("invalid input reached API", suffix)
		}
	}
}
