package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func TestCmdObsNotificationHealth(t *testing.T) {
	out, stderr := obsBundleTestStreams(t)
	zero := 0.0
	response := api.ObsNotificationHealth{GeneratedAt: time.Now().UTC(), State: "partial", WindowSeconds: 600, AlertmanagerAPI: api.ObsNotificationAPIStatus{State: "disabled"},
		Signals: map[string]api.ObsNotificationSignal{"failed": {State: "no_samples"}, "attempts": {State: "healthy", Value: &zero}}, Integrations: []api.ObsNotificationIntegration{}, Limitations: []string{"no_per_episode_delivery_receipt"}}
	mode := "ok"
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != operatorobs.NotificationHealthPath || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("request: %s %s", r.Method, r.URL)
		}
		if mode == "error" {
			http.Error(w, "private upstream body", 503)
			return
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer srv.Close()
	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "token")
	if code := cmdObsDispatch([]string{subObsNotificationHealth}); code != 0 || !strings.Contains(out.String(), "state=no_samples value=unknown") || !strings.Contains(out.String(), "state=healthy value=0") || !strings.Contains(out.String(), "per_episode_delivery=unknown") {
		t.Fatalf("human: %d %s %s", code, out, stderr)
	}
	out.Reset()
	if code := cmdObsNotificationHealth([]string{"--json"}); code != 0 || !strings.Contains(out.String(), `"value": null`) {
		t.Fatalf("json: %d %s %s", code, out, stderr)
	}
	before := calls
	for _, args := range [][]string{{"--timeout", "0s"}, {"extra"}, {"--since", "bad"}} {
		if code := cmdObsNotificationHealth(args); code != 2 {
			t.Fatalf("args: %d", code)
		}
	}
	if calls != before {
		t.Fatal("invalid args made requests")
	}
	t.Setenv("FAAS_ADMIN_TOKEN", "")
	if code := cmdObsNotificationHealth(nil); code != 2 || calls != before {
		t.Fatal("missing token made request")
	}
	t.Setenv("FAAS_ADMIN_TOKEN", "token")
	mode = "error"
	out.Reset()
	stderr.Reset()
	if code := cmdObsNotificationHealth(nil); code != 1 || out.Len() != 0 || strings.Contains(stderr.String(), "private") {
		t.Fatalf("error: %d %s %s", code, out, stderr)
	}
	mode = "ok"
	response.Signals = nil
	if code := cmdObsNotificationHealth(nil); code != 1 {
		t.Fatal("invalid response accepted")
	}
}
