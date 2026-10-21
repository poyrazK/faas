package main_test

// adr: 749 — `gregale channels` lists, tests and adds alert notification
// channels; secret destinations are only read from stdin.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func channelsTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/notification-channels":
			_ = json.NewEncoder(w).Encode([]api.NotificationChannelResponse{
				{ID: "ch-1", Name: "ops", Kind: "slack", Target: "hooks.slack.com/T1/B1/…", LastDeliveredAt: "2026-10-13T09:00:00Z"},
				{ID: "ch-2", Name: "pager", Kind: "pagerduty", Target: "…abcd", LastDeliveredAt: "2026-10-13T08:00:00Z", LastErrorAt: "2026-10-13T09:30:00Z", LastError: "destination rejected the notification (404)"},
			})
		case "POST /v1/notification-channels/ch-1/test":
			_ = json.NewEncoder(w).Encode(api.TestNotificationChannelResponse{Delivered: true})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	return srv
}

func captureChannelsStdout(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := (*main.OsStdout)
	(*main.OsStdout) = &buf
	t.Cleanup(func() { (*main.OsStdout) = prev })
	return &buf
}

func TestCmdChannelsJSONOutput(t *testing.T) {
	main.ResetJSONOutput()
	channelsTestServer(t)
	buf := captureChannelsStdout(t)
	(*main.JsonOutput) = true
	defer func() { main.ResetJSONOutput() }()

	if code := main.CmdChannels([]string{"list"}); code != 0 {
		t.Fatalf("gregale channels list --json = %d", code)
	}
	var list []api.NotificationChannelResponse
	if err := json.Unmarshal(buf.Bytes(), &list); err != nil || len(list) != 2 || list[1].LastError == "" {
		t.Fatalf("list --json = %v %s", err, buf.String())
	}

	buf.Reset()
	if code := main.CmdChannels([]string{"test", "ch-1"}); code != 0 {
		t.Fatalf("gregale channels test --json = %d", code)
	}
	var res api.TestNotificationChannelResponse
	if err := json.Unmarshal(buf.Bytes(), &res); err != nil || !res.Delivered {
		t.Fatalf("test --json = %v %s", err, buf.String())
	}
}

func TestCmdChannelsListText(t *testing.T) {
	main.ResetJSONOutput()
	channelsTestServer(t)
	buf := captureChannelsStdout(t)
	if code := main.CmdChannels([]string{"list"}); code != 0 {
		t.Fatalf("gregale channels list = %d", code)
	}
	out := buf.String()
	for _, want := range []string{"ok 2026-10-13T09:00:00Z", "FAILED 2026-10-13T09:30:00Z: destination rejected the notification (404)"} {
		if !strings.Contains(out, want) {
			t.Errorf("list output lacks %q:\n%s", want, out)
		}
	}
}

func TestCmdChannelsArgumentErrors(t *testing.T) {
	main.ResetJSONOutput()
	channelsTestServer(t)
	captureChannelsStdout(t)
	tests := []struct {
		name string
		args []string
	}{
		{"no subcommand", nil},
		{"unknown subcommand", []string{"mute"}},
		{"add without name", []string{"add", "--kind", "email"}},
		{"slack secret on the command line", []string{"add", "--name", "ops", "--kind", "slack"}},
		{"pagerduty secret on the command line", []string{"add", "--name", "pd", "--kind", "pagerduty"}},
		{"test without id", []string{"test"}},
		{"rm with two ids", []string{"rm", "a", "b"}},
	}
	for _, tt := range tests {
		if code := main.CmdChannels(tt.args); code == 0 {
			t.Errorf("%s: exit 0, want an error", tt.name)
		}
	}
}
