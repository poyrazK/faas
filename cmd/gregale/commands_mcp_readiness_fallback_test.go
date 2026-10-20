package main_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPReadinessFallbackCLI(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, `{"version":1,"enabled":true}`, http.StatusOK)
	id := "00000000-0000-0000-0000-000000000001"
	base := []string{"--app", "demo", "--rule", id}
	if code := main.CmdMCPReadiness(append([]string{"fallback", "get"}, base...)); code != 0 || (*f.SawPath()) != "/v1/apps/demo/alerts/"+id+"/mcp-readiness-fallback" {
		t.Fatal(code, (*f.SawPath()))
	}
	t.Setenv("MCP_FALLBACK_SIGNING_SECRET", "private-signing-material")
	args := append(append([]string{"set"}, base...), "--expected-version", "0", "--webhook-url", "https://fallback.example.com/hook", "--secret-env", "MCP_FALLBACK_SIGNING_SECRET", "--after-failures", "2")
	if code := main.CmdMCPReadinessFallback(args); code != 0 || (*f.SawMethod()) != "PUT" {
		t.Fatal(code)
	}
	var req api.SetMCPReadinessFallbackRequest
	if err := json.Unmarshal((*f.SawBody()), &req); err != nil || req.ExpectedVersion == nil || *req.ExpectedVersion != 0 || req.Enabled == nil || !*req.Enabled || req.WebhookSecret != "private-signing-material" || req.AfterFailures != 2 {
		t.Fatal(req, err)
	}
	if code := main.CmdMCPReadinessFallback(append(append([]string{"disable"}, base...), "--expected-version", "1")); code != 0 {
		t.Fatal(code)
	}
	for _, suffix := range [][]string{{"get", "--expected-version", "0"}, {"disable"}, {"set", "--expected-version", "0", "--webhook-url", "https://fallback.example.com", "--secret-env", "MISSING"}, {"disable", "--expected-version", "0", "--secret-env", "MCP_FALLBACK_SIGNING_SECRET"}} {
		(*f.SawMethod()) = ""
		if code := main.CmdMCPReadinessFallback(append(suffix, base...)); code == 0 || (*f.SawMethod()) != "" {
			t.Fatal("invalid input reached API", suffix)
		}
	}
}
