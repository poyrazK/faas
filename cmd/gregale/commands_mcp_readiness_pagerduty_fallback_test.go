package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPReadinessPagerDutyFallbackCLI(t *testing.T) {
	main.ResetJSONOut(t)
	t.Setenv("MCP_NATIVE_FALLBACK_TEST_KEY", strings.Repeat("a", 32))
	t.Setenv("MCP_NATIVE_FALLBACK_TEST_SECRET", strings.Repeat("s", 32))
	f := main.AuthedFakeAPI(t, `{"version":1,"enabled":true,"provider":"pagerduty","region":"eu"}`, http.StatusOK)
	base := []string{"--app", "demo", "--rule", "00000000-0000-0000-0000-000000000001"}
	args := append(append([]string{"set"}, base...), "--expected-version", "0", "--provider", "pagerduty", "--region", "eu", "--key-env", "MCP_NATIVE_FALLBACK_TEST_KEY", "--after-failures", "2")
	if code := main.CmdMCPReadinessFallback(args); code != 0 {
		t.Fatal(code)
	}
	var req api.SetMCPReadinessFallbackRequest
	if err := json.Unmarshal((*f.SawBody()), &req); err != nil || req.Provider != "pagerduty" || req.Region != "eu" || req.IntegrationKey != strings.Repeat("a", 32) || req.WebhookSecret != "" || req.WebhookURL != "" {
		t.Fatal("native fallback payload mismatch", err)
	}
	bind := append(append([]string{"set"}, base...), "--expected-version", "0", "--destination", "fallback", "--expected-fallback-version", "1", "--service-id", "PSVC001", "--secret-env", "MCP_NATIVE_FALLBACK_TEST_SECRET")
	if code := main.CmdMCPReadinessPagerDutyAcknowledgements(bind); code != 0 {
		t.Fatal(code)
	}
	var binding api.SetMCPReadinessPagerDutyAcknowledgementsRequest
	if err := json.Unmarshal((*f.SawBody()), &binding); err != nil || binding.Destination != "fallback" || binding.ExpectedFallbackVersion == nil || *binding.ExpectedFallbackVersion != 1 || binding.ExpectedPagerDutyVersion != nil {
		t.Fatal("fallback binding mismatch", err)
	}
	for _, suffix := range [][]string{{"--secret-env", "MCP_NATIVE_FALLBACK_TEST_SECRET"}, {"--webhook-url", "https://example.com/hook"}, {"--key-env", "MISSING_KEY"}, {"--region", "ap"}} {
		(*f.SawMethod()) = ""
		if code := main.CmdMCPReadinessFallback(append(append([]string{}, args...), suffix...)); code == 0 || (*f.SawMethod()) != "" {
			t.Fatal("invalid fallback reached API", suffix)
		}
	}
	(*f.SawMethod()) = ""
	if code := main.CmdMCPReadinessPagerDutyAcknowledgements(append(bind, "--expected-pagerduty-version", "1")); code == 0 || (*f.SawMethod()) != "" {
		t.Fatal("mixed version flags reached API")
	}
}
