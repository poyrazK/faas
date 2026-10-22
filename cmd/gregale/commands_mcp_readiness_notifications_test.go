package main_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPReadinessNotificationsCLI(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, `{"queues":[]}`, http.StatusOK)
	if code := main.CmdMCPReadiness([]string{"notifications", "list", "--app", "demo", "--scope", "staging"}); code != 0 || (*f.SawPath()) != "/v1/apps/demo/mcp-readiness-notifications" {
		t.Fatal(code, (*f.SawPath()))
	}
	event := "00000000-0000-0000-0000-000000000001"
	request := "00000000-0000-0000-0000-000000000002"
	for _, action := range []string{"retry", "abandon"} {
		if code := main.CmdMCPReadinessNotifications([]string{action, "--app", "demo", "--event", event, "--request-id", request, "--expected-version", "2", "--reason", "repaired"}); code != 0 || (*f.SawPath()) != "/v1/apps/demo/mcp-readiness-notifications/actions" {
			t.Fatal(code, (*f.SawPath()))
		}
		var r api.MCPReadinessNotificationAction
		if err := json.Unmarshal((*f.SawBody()), &r); err != nil || r.RequestID != request || r.EventID != event || *r.ExpectedVersion != 2 || r.Action != action {
			t.Fatal(r, err)
		}
	}
	for _, args := range [][]string{{"retry", "--event", event, "--expected-version", "0", "--reason", "repaired"}, {"abandon", "--event", event, "--request-id", request, "--reason", "retired"}, {"list", "--event", event}, {"list", "--after", "bad"}, {"list", "--limit", "0"}, {"retry", "--event", "bad"}, {"abandon", "--scope", "staging"}} {
		(*f.SawMethod()) = ""
		args = append(args, "--app", "demo")
		if code := main.CmdMCPReadinessNotifications(args); code == 0 || (*f.SawMethod()) != "" {
			t.Fatal("invalid input reached API", args, code)
		}
	}
}

func TestMCPReadinessNotificationDestinationCLI(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, `{"destination":"fallback"}`, http.StatusOK)
	args := []string{"retry", "--app", "demo", "--event", "00000000-0000-0000-0000-000000000001", "--request-id", "00000000-0000-0000-0000-000000000002", "--expected-version", "0", "--reason", "Repair", "--destination", "fallback"}
	if code := main.CmdMCPReadinessNotifications(args); code != 0 {
		t.Fatal(code)
	}
	var request api.MCPReadinessNotificationAction
	if err := json.Unmarshal((*f.SawBody()), &request); err != nil || request.Destination != "fallback" {
		t.Fatal(request, err)
	}
	(*f.SawMethod()) = ""
	args[len(args)-1] = "unknown"
	if code := main.CmdMCPReadinessNotifications(args); code == 0 || (*f.SawMethod()) != "" {
		t.Fatal("unknown destination reached API")
	}
}
