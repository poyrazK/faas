package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPReadinessReminderDefaultsCLI(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"version":1,"reminder_policy":{"interval_seconds":60,"max_reminders":2}}`, http.StatusOK)
	id := "00000000-0000-0000-0000-000000000001"
	base := []string{"--app", "demo", "--rule", id}
	if code := cmdMCPReadiness(append([]string{"reminder-defaults", "get"}, base...)); code != 0 || f.sawMethod != "GET" || f.sawPath != "/v1/apps/demo/alerts/"+id+"/mcp-readiness-reminder-defaults" {
		t.Fatal(code, f.sawPath)
	}
	args := append(append([]string{"set"}, base...), "--expected-version", "0", "--interval-seconds", "60", "--max-reminders", "2")
	if code := cmdMCPReadinessReminderDefaults(args); code != 0 || f.sawMethod != "PUT" {
		t.Fatal(code)
	}
	var req api.SetMCPReadinessReminderDefaultsRequest
	if e := json.Unmarshal(f.sawBody, &req); e != nil || req.ExpectedVersion == nil || *req.ExpectedVersion != 0 || req.ReminderPolicy == nil || req.ReminderPolicy.MaxReminders != 2 {
		t.Fatal(req, e)
	}
	if code := cmdMCPReadinessReminderDefaults(append(append([]string{"disable"}, base...), "--expected-version", "1")); code != 0 {
		t.Fatal(code)
	}
	if e := json.Unmarshal(f.sawBody, &req); e != nil || req.ReminderPolicy == nil || *req.ReminderPolicy != (api.MCPReadinessReminderPolicy{}) {
		t.Fatal(req, e)
	}
	for _, suffix := range [][]string{{"get", "--expected-version", "0"}, {"disable"}, {"set", "--expected-version", "0"}, {"set", "--expected-version", "0", "--interval-seconds", "59", "--max-reminders", "2"}, {"disable", "--expected-version", "0", "--max-reminders", "0"}} {
		f.sawMethod = ""
		if code := cmdMCPReadinessReminderDefaults(append(suffix, base...)); code == 0 || f.sawMethod != "" {
			t.Fatal("invalid reached API", suffix)
		}
	}
}
