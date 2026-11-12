package main_test

// adr: 20261104142830 — `gregale alert channel update` renames a channel or
// replaces its destination in place, keeping its id and bindings.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdChannelsUpdateRenames(t *testing.T) {
	main.ResetJSONOutput()
	var got api.UpdateNotificationChannelRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/v1/notification-channels/ch-1" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.NotificationChannelResponse{ID: "ch-1", Name: *got.Name, Kind: "slack", Target: "hooks.slack.com/T1/B1/…"})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	buf := captureChannelsStdout(t)
	(*main.JsonOutput) = true
	defer func() { main.ResetJSONOutput() }()

	if code := main.CmdChannels([]string{"update", "--name", "oncall", "ch-1"}); code != 0 {
		t.Fatalf("gregale alert channel update = %d", code)
	}
	if got.Name == nil || *got.Name != "oncall" || got.SlackWebhookURL != "" {
		t.Fatalf("request = %+v; want only the new name", got)
	}
	var ch api.NotificationChannelResponse
	if err := json.Unmarshal(buf.Bytes(), &ch); err != nil || ch.Name != "oncall" {
		t.Fatalf("output = %s (%v)", buf.String(), err)
	}
	if code := main.CmdChannels([]string{"update", "ch-1"}); code == 0 {
		t.Fatal("update without --name or --secret-stdin succeeded")
	}
}
