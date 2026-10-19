package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPReadinessPagerDutyCLI(t *testing.T) {
	resetJSONOut(t)
	t.Setenv("PAGERDUTY_TEST_KEY", strings.Repeat("a", 32))
	f := authedFakeAPI(t, `{"version":1,"enabled":true,"integration_key_masked":"***"}`, http.StatusOK)
	id := "00000000-0000-0000-0000-000000000001"
	base := []string{"--app", "demo", "--rule", id}
	if code := cmdMCPReadiness(append([]string{"pagerduty", "get"}, base...)); code != 0 || f.sawMethod != "GET" || f.sawPath != "/v1/apps/demo/alerts/"+id+"/mcp-readiness-pagerduty" {
		t.Fatal(code, f.sawPath)
	}
	args := append(append([]string{"set"}, base...), "--expected-version", "0", "--key-env", "PAGERDUTY_TEST_KEY", "--region", "eu")
	if code := cmdMCPReadinessPagerDuty(args); code != 0 || f.sawMethod != "PUT" {
		t.Fatal(code)
	}
	var req api.SetMCPReadinessPagerDutyRequest
	if e := json.Unmarshal(f.sawBody, &req); e != nil || req.ExpectedVersion == nil || *req.ExpectedVersion != 0 || req.Enabled == nil || !*req.Enabled || req.IntegrationKey != strings.Repeat("a", 32) || req.Region != "eu" {
		t.Fatal(req, e)
	}
	if code := cmdMCPReadinessPagerDuty(append(append([]string{"disable"}, base...), "--expected-version", "1")); code != 0 {
		t.Fatal(code)
	}
	req = api.SetMCPReadinessPagerDutyRequest{}
	if e := json.Unmarshal(f.sawBody, &req); e != nil || req.Enabled == nil || *req.Enabled || req.IntegrationKey != "" {
		t.Fatal(req, e)
	}
	for _, suffix := range [][]string{{"get", "--region", "eu"}, {"disable", "--expected-version", "1", "--region", "us"}, {"set", "--expected-version", "0", "--key-env", "PAGERDUTY_TEST_KEY", "--region", "ap"}, {"get", "--expected-version", "0"}, {"disable"}, {"set", "--expected-version", "0"}, {"set", "--expected-version", "0", "--key-env", "MISSING_KEY"}, {"disable", "--expected-version", "0", "--key-env", "PAGERDUTY_TEST_KEY"}} {
		f.sawMethod = ""
		if code := cmdMCPReadinessPagerDuty(append(suffix, base...)); code == 0 || f.sawMethod != "" {
			t.Fatal("invalid reached API", suffix)
		}
	}
}
