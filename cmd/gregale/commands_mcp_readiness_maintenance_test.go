package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMCPReadinessMaintenanceCLI(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, `{"id":"00000000-0000-0000-0000-000000000001"}`, http.StatusCreated)
	if code := main.CmdMCPReadiness([]string{"maintenance", "create", "--app", "demo", "--scope", "staging", "--duration", "30m", "--reason", "upgrade"}); code != 0 || (*f.SawPath()) != "/v1/apps/demo/mcp-readiness-maintenance" {
		t.Fatal("create", code, (*f.SawPath()))
	}
	var req api.CreateMCPReadinessMaintenanceRequest
	if err := json.Unmarshal((*f.SawBody()), &req); err != nil || req.Scope != "staging" || req.EndsAt.Sub(req.StartsAt).Minutes() != 30 {
		t.Fatal(req, err)
	}
	for _, args := range [][]string{{"create", "--duration", "0s", "--reason", "upgrade"}, {"create", "--duration", "721h", "--reason", "upgrade"}, {"create", "--duration", "1h", "--reason", ""}, {"list", "--before", "bad"}, {"cancel", "--id", "bad", "--reason", "complete"}, {"list", "--duration", "1h"}} {
		(*f.SawMethod()) = ""
		args = append(args, "--app", "demo")
		if code := main.CmdMCPReadinessMaintenance(args); code == 0 || (*f.SawMethod()) != "" {
			t.Fatal("invalid input reached API", args, code)
		}
	}
}

// Each invalid input names what to change; on production one sentence
// covered every rule, including a cancel that only lacked --reason.
func TestMCPReadinessMaintenanceCLI_SaysWhichFlagIsWrong(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, `{}`, http.StatusOK)
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"cancel", "--id", "00000000-0000-0000-0000-000000000001"}, "--reason is required"},
		{[]string{"cancel", "--id", "bad", "--reason", "done"}, "--id must be a window id"},
		{[]string{"create", "--reason", "upgrade"}, "an end is required"},
		{[]string{"create", "--duration", "721h", "--reason", "upgrade"}, "at most 30 days"},
		{[]string{"create", "--duration", "1h", "--end", "2030-01-01T00:00:00Z", "--reason", "upgrade"}, "not both"},
		{[]string{"create", "--start", "2020-01-01T00:00:00Z", "--duration", "1h", "--reason", "upgrade"}, "start is in the past"},
		{[]string{"list", "--duration", "1h"}, "--duration does not apply to maintenance list"},
	} {
		args := append(tt.args, "--app", "demo")
		(*f.SawMethod()) = ""
		stderr := captureMaintenanceStderr(t, func() {
			if code := main.CmdMCPReadinessMaintenance(args); code == 0 {
				t.Errorf("%v: exit 0", args)
			}
		})
		if (*f.SawMethod()) != "" || !strings.Contains(stderr, tt.want) {
			t.Errorf("%v: stderr %q, want %q (and no API call, saw %q)", args, stderr, tt.want, *f.SawMethod())
		}
	}
}

func TestMCPReadinessMaintenanceCLI_ListIsATable(t *testing.T) {
	main.ResetJSONOut(t)
	main.AuthedFakeAPI(t, `{"windows":[{"id":"00000000-0000-0000-0000-000000000001","scope":"default","starts_at":"2026-11-09T10:00:00Z","ends_at":"2026-11-09T11:00:00Z","reason":"db upgrade","cancelled_at":"2026-11-09T10:30:00Z"}]}`, http.StatusOK)
	var out bytes.Buffer
	prev := *main.OsStdout
	*main.OsStdout = &out
	defer func() { *main.OsStdout = prev }()
	if code := main.CmdMCPReadinessMaintenance([]string{"list", "--app", "demo"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	got := out.String()
	if strings.HasPrefix(strings.TrimSpace(got), "{") || !strings.Contains(got, "cancelled") || !strings.Contains(got, "db upgrade") {
		t.Fatalf("list output = %q, want a table with state and reason", got)
	}
}

func captureMaintenanceStderr(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := *main.OsStderr
	*main.OsStderr = &buf
	defer func() { *main.OsStderr = prev }()
	fn()
	return buf.String()
}
