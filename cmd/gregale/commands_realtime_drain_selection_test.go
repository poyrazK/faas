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

// Hunt 32 H32-71: the CLI always sent its default --limit, so a drain with
// only --reason closed every open connection although the server refuses a
// drain that names no connections (#6838).
func TestCmdRealtimeDrainNeedsASelection(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, `{}`, http.StatusAccepted)
	oldErr := *main.OsStderr
	var stderr bytes.Buffer
	*main.OsStderr = &stderr
	t.Cleanup(func() { *main.OsStderr = oldErr })

	if code := main.CmdRealtimeDrain([]string{"demo", "endpoint-1", "--reason", "h32"}); code == 0 {
		t.Fatal("a reason-only drain succeeded")
	}
	if *f.SawMethod() != "" {
		t.Fatalf("a reason-only drain reached the API: %s %s", *f.SawMethod(), *f.SawPath())
	}
	if !strings.Contains(stderr.String(), "--channel, --principal, --connection-id, --limit N, or --all") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestCmdRealtimeDrainFilterKeepsServerDefaultLimit(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, `{"operation_id":"drain-1","status":"running","results":[],"matched":0,"limit":100}`, http.StatusAccepted)
	oldOut := *main.OsStdout
	*main.OsStdout = &bytes.Buffer{}
	t.Cleanup(func() { *main.OsStdout = oldOut })

	if code := main.CmdRealtimeDrain([]string{"demo", "endpoint-1", "--reason", "deploy", "--channel", "room-a"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	var request api.ManagedRealtimeDrainRequest
	if err := json.Unmarshal(*f.SawBody(), &request); err != nil {
		t.Fatal(err)
	}
	if request.Limit != 0 || request.Channel != "room-a" {
		t.Fatalf("request = %+v, want the channel and no limit", request)
	}
}

// Hunt 32 H32-71: create took --enabled[=bool] while update needed
// --enable/--disable.
func TestCmdRealtimeUpdateAcceptsEnabledFlag(t *testing.T) {
	for _, tt := range []struct {
		arg  string
		want bool
	}{{"--enabled=false", false}, {"--enabled", true}, {"--enabled=true", true}} {
		t.Run(tt.arg, func(t *testing.T) {
			main.ResetJSONOut(t)
			f := main.AuthedFakeAPI(t, `{"id":"endpoint-1","app_id":"app-1","enabled":true}`, http.StatusOK)
			oldOut := *main.OsStdout
			*main.OsStdout = &bytes.Buffer{}
			t.Cleanup(func() { *main.OsStdout = oldOut })
			if code := main.CmdRealtimeUpdate([]string{"demo", "endpoint-1", tt.arg}); code != 0 {
				t.Fatalf("exit = %d", code)
			}
			var request struct {
				Enabled *bool `json:"enabled"`
			}
			if err := json.Unmarshal(*f.SawBody(), &request); err != nil {
				t.Fatal(err)
			}
			if request.Enabled == nil || *request.Enabled != tt.want {
				t.Fatalf("enabled = %v, want %v", request.Enabled, tt.want)
			}
		})
	}
	main.ResetJSONOut(t)
	main.AuthedFakeAPI(t, `{}`, http.StatusOK)
	if code := main.CmdRealtimeUpdate([]string{"demo", "endpoint-1", "--enabled=false", "--enable"}); code == 0 {
		t.Fatal("--enabled=false with --enable succeeded")
	}
}
