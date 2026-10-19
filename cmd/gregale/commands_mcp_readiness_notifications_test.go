package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPReadinessNotificationsCLI(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"queues":[]}`, http.StatusOK)
	if code := cmdMCPReadiness([]string{"notifications", "list", "--app", "demo", "--scope", "staging"}); code != 0 || f.sawPath != "/v1/apps/demo/mcp-readiness-notifications" {
		t.Fatal(code, f.sawPath)
	}
	event := "00000000-0000-0000-0000-000000000001"
	request := "00000000-0000-0000-0000-000000000002"
	for _, action := range []string{"retry", "abandon"} {
		if code := cmdMCPReadinessNotifications([]string{action, "--app", "demo", "--event", event, "--request-id", request, "--expected-version", "2", "--reason", "repaired"}); code != 0 || f.sawPath != "/v1/apps/demo/mcp-readiness-notifications/actions" {
			t.Fatal(code, f.sawPath)
		}
		var r api.MCPReadinessNotificationAction
		if err := json.Unmarshal(f.sawBody, &r); err != nil || r.RequestID != request || r.EventID != event || *r.ExpectedVersion != 2 || r.Action != action {
			t.Fatal(r, err)
		}
	}
	for _, args := range [][]string{{"retry", "--event", event, "--expected-version", "0", "--reason", "repaired"}, {"abandon", "--event", event, "--request-id", request, "--reason", "retired"}, {"list", "--event", event}, {"list", "--after", "bad"}, {"list", "--limit", "0"}, {"retry", "--event", "bad"}, {"abandon", "--scope", "staging"}} {
		f.sawMethod = ""
		args = append(args, "--app", "demo")
		if code := cmdMCPReadinessNotifications(args); code == 0 || f.sawMethod != "" {
			t.Fatal("invalid input reached API", args, code)
		}
	}
}

func TestMCPReadinessNotificationDestinationCLI(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"destination":"fallback"}`, http.StatusOK)
	args := []string{"retry", "--app", "demo", "--event", "00000000-0000-0000-0000-000000000001", "--request-id", "00000000-0000-0000-0000-000000000002", "--expected-version", "0", "--reason", "Repair", "--destination", "fallback"}
	if code := cmdMCPReadinessNotifications(args); code != 0 {
		t.Fatal(code)
	}
	var request api.MCPReadinessNotificationAction
	if err := json.Unmarshal(f.sawBody, &request); err != nil || request.Destination != "fallback" {
		t.Fatal(request, err)
	}
	f.sawMethod = ""
	args[len(args)-1] = "unknown"
	if code := cmdMCPReadinessNotifications(args); code == 0 || f.sawMethod != "" {
		t.Fatal("unknown destination reached API")
	}
}
