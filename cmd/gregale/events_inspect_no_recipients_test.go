package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Production-us rc.294: inspecting an event no subscription matched printed
// "Recipients: 0" and then a bare table header (Hunt 14 H14-47).
func TestEventsInspectSaysWhenNoSubscriptionMatched(t *testing.T) {
	main.ResetJSONOut(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/events/receipt" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"event_id":"ev-1","event_source":"h14.e2e","event_type":"h14.ping","accepted_at":"2026-10-25T06:41:08Z","snapshot_captured":true,"recipient_count":0,"recipients":[]}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	out, restore := main.CaptureStdout(t)
	code := main.Run([]string{"events", "inspect", "--source", "h14.e2e", "--id", "ev-1"})
	restore()
	got := out.String()
	if code != 0 || strings.Contains(got, "SUBSCRIPTION\tROUTING") || !strings.Contains(got, "No subscription matched this event") {
		t.Fatalf("exit %d; want the no-match line, not an empty table:\n%s", code, got)
	}
}
