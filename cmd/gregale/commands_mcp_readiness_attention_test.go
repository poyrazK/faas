package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPReadinessAttentionCLI(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"incidents":[]}`, http.StatusOK)
	if code := cmdMCPReadiness([]string{"attention", "list", "--app", "demo", "--scope", "staging", "--include-resolved"}); code != 0 || f.sawPath != "/v1/apps/demo/mcp-readiness-attention" {
		t.Fatal(code, f.sawPath)
	}
	id := "00000000-0000-0000-0000-000000000001"
	request := "00000000-0000-0000-0000-000000000002"
	base := []string{"--app", "demo", "--incident", id, "--request-id", request, "--expected-version", "0", "--reason", "Investigating"}
	for _, action := range []string{"acknowledge", "release", "configure"} {
		args := append([]string{action}, base...)
		if action == "acknowledge" {
			args = append(args, "--expires-at", "2026-10-18T12:00:00+03:00")
		}
		if action == "configure" {
			args = append(args, "--interval", "0s", "--max-reminders", "0")
		}
		if code := cmdMCPReadinessAttention(args); code != 0 || f.sawPath != "/v1/apps/demo/mcp-readiness-incidents/"+id+"/attention-actions" {
			t.Fatal(code, f.sawPath)
		}
		var r api.MCPReadinessAttentionAction
		if err := json.Unmarshal(f.sawBody, &r); err != nil || r.RequestID != request || r.ExpectedVersion == nil || *r.ExpectedVersion != 0 {
			t.Fatal(r, err)
		}
		if action == "configure" && (r.Action != "configure_reminders" || r.ReminderPolicy == nil || r.ReminderPolicy.MaxReminders != 0) {
			t.Fatal(r)
		}
	}
	for _, suffix := range [][]string{{"--interval", "1m", "--max-reminders", "3"}, {"--interval", "24h", "--max-reminders", "10"}} {
		args := append(append([]string{"configure"}, base...), suffix...)
		if code := cmdMCPReadinessAttention(args); code != 0 {
			t.Fatal(args, code)
		}
	}
	for _, args := range [][]string{{"acknowledge", "--incident", id, "--expected-version", "0", "--reason", "Investigating"}, {"release", "--incident", id, "--request-id", request, "--reason", "Done"}, {"list", "--incident", id}, {"list", "--after", "bad"}, {"history", "--incident", id, "--before", "bad"}, {"history", "--incident", id, "--scope", "staging"}, {"configure", "--incident", id, "--expected-version", "0", "--request-id", request, "--reason", "x", "--interval", "1m"}} {
		f.sawMethod = ""
		args = append(args, "--app", "demo")
		if code := cmdMCPReadinessAttention(args); code == 0 || f.sawMethod != "" {
			t.Fatal("invalid input reached API", args, code)
		}
	}
	for _, suffix := range [][]string{{"--interval", "60.5s", "--max-reminders", "3"}, {"--interval", "1m", "--max-reminders", "0"}, {"--interval", "25h", "--max-reminders", "3"}, {"--interval", "1m", "--max-reminders", "11"}} {
		f.sawMethod = ""
		args := append(append([]string{"configure"}, base...), suffix...)
		if code := cmdMCPReadinessAttention(args); code == 0 || f.sawMethod != "" {
			t.Fatal(args, code)
		}
	}
}
