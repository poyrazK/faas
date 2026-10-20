package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPReadinessPagerDutyAcknowledgementsCLI(t *testing.T) {
	main.ResetJSONOut(t)
	t.Setenv("PAGERDUTY_WEBHOOK_TEST_SECRET", strings.Repeat("s", 32))
	f := main.AuthedFakeAPI(t, `{"version":1,"enabled":true,"webhook_path":"/v1/webhooks/mcp-readiness/pagerduty/rule"}`, http.StatusOK)
	id := "00000000-0000-0000-0000-000000000001"
	base := []string{"--app", "demo", "--rule", id}
	if code := main.CmdMCPReadinessPagerDuty(append([]string{"acknowledgements", "get"}, base...)); code != 0 || (*f.SawPath()) != "/v1/apps/demo/alerts/"+id+"/mcp-readiness-pagerduty-acknowledgements" {
		t.Fatal(code, (*f.SawPath()))
	}
	args := append(append([]string{"set"}, base...), "--expected-version", "0", "--expected-pagerduty-version", "1", "--service-id", "PSVC001", "--secret-env", "PAGERDUTY_WEBHOOK_TEST_SECRET", "--sync-unacknowledgements")
	if code := main.CmdMCPReadinessPagerDutyAcknowledgements(args); code != 0 {
		t.Fatal(code)
	}
	var req api.SetMCPReadinessPagerDutyAcknowledgementsRequest
	if err := json.Unmarshal((*f.SawBody()), &req); err != nil || !req.SyncUnacknowledgements || req.ExpectedPagerDutyVersion == nil || *req.ExpectedPagerDutyVersion != 1 || req.SigningSecret != strings.Repeat("s", 32) || req.ServiceID != "PSVC001" {
		t.Fatal("binding lost", err)
	}
	if code := main.CmdMCPReadinessPagerDutyAcknowledgements(append(append([]string{"disable"}, base...), "--expected-version", "1")); code != 0 {
		t.Fatal(code)
	}
	var disabled map[string]any
	if err := json.Unmarshal((*f.SawBody()), &disabled); err != nil || len(disabled) != 2 || disabled["enabled"] != false {
		t.Fatal(disabled, err)
	}
	if code := main.CmdMCPReadinessPagerDutyAcknowledgements(append(append([]string{"events"}, base...), "--limit", "1")); code != 0 || !strings.HasSuffix((*f.SawPath()), "/mcp-readiness-pagerduty-acknowledgements/events") || (*f.SawQuery()) != "limit=1" {
		t.Fatal(code, (*f.SawPath()))
	}

	if code := main.CmdMCPReadinessPagerDutyAcknowledgements(append(append([]string{"get"}, base...), "--destination", "fallback")); code != 0 || (*f.SawQuery()) != "destination=fallback" {
		t.Fatal(code, (*f.SawQuery()))
	}
	if code := main.CmdMCPReadinessPagerDutyAcknowledgements(append(append([]string{"disable"}, base...), "--expected-version", "1", "--destination", "fallback")); code != 0 {
		t.Fatal(code)
	}
	if err := json.Unmarshal((*f.SawBody()), &disabled); err != nil || disabled["destination"] != "fallback" || disabled["enabled"] != false {
		t.Fatal(disabled, err)
	}
	for _, suffix := range [][]string{{"get", "--sync-unacknowledgements"}, {"disable", "--expected-version", "0", "--sync-unacknowledgements"}, {"get", "--expected-version", "0"}, {"disable", "--expected-version", "0", "--service-id", "PSVC001"}, {"set", "--expected-version", "0", "--expected-pagerduty-version", "1", "--service-id", "PSVC001", "--secret-env", "MISSING_SECRET"}, {"events", "--limit", "0"}, {"events", "--before", "bad"}} {
		(*f.SawMethod()) = ""
		if code := main.CmdMCPReadinessPagerDutyAcknowledgements(append(suffix, base...)); code == 0 || (*f.SawMethod()) != "" {
			t.Fatal("invalid request reached API", suffix)
		}
	}
}
