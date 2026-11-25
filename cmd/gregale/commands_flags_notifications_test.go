package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// fakeFlagNotificationsAPI keeps one environment's notification channels.
type fakeFlagNotificationsAPI struct {
	mu       sync.Mutex
	channels []string
}

func (f *fakeFlagNotificationsAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path != "/v1/projects/exports/environments/production/flags/notifications" {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodPut {
		var body struct {
			ChannelIDs []string `json:"channel_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ChannelIDs == nil {
			http.Error(w, "channel_ids required", http.StatusBadRequest)
			return
		}
		f.channels = body.ChannelIDs
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"channel_ids": f.channels})
}

func runFlagNotifyCLI(t *testing.T, api http.Handler, args ...string) (int, string) {
	t.Helper()
	server := httptest.NewServer(api)
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	oldOut, oldJSON := (*main.OsStdout), (*main.JsonOutput)
	var out bytes.Buffer
	(*main.OsStdout), (*main.JsonOutput) = &out, false
	defer func() { (*main.OsStdout), (*main.JsonOutput) = oldOut, oldJSON }()
	code := main.CmdFlags(append(append([]string{"notifications"}, args...), "--project", "exports"))
	return code, out.String()
}

// ADR-20261125085913: flag notify get, set and clear an environment's
// notification channels.
func TestFlagNotifySetGetClear(t *testing.T) {
	api := &fakeFlagNotificationsAPI{channels: []string{}}
	if code, out := runFlagNotifyCLI(t, api, "get"); code != 0 || !strings.Contains(out, "are off") {
		t.Fatalf("get before set: %d %q", code, out)
	}
	if code, out := runFlagNotifyCLI(t, api, "set", "--channel", "ch-1", "--channel", "ch-2"); code != 0 || !strings.Contains(out, "go to: ch-1, ch-2") {
		t.Fatalf("set: %d %q", code, out)
	}
	if code, out := runFlagNotifyCLI(t, api, "get"); code != 0 || !strings.Contains(out, "ch-1, ch-2") {
		t.Fatalf("get: %d %q", code, out)
	}
	if code, out := runFlagNotifyCLI(t, api, "clear"); code != 0 || !strings.Contains(out, "are off") || len(api.channels) != 0 {
		t.Fatalf("clear: %d %q (channels %v)", code, out, api.channels)
	}
	if code, _ := runFlagNotifyCLI(t, api, "set"); code == 0 {
		t.Fatal("set without --channel succeeded")
	}
}
