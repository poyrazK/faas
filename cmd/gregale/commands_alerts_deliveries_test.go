package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAlertsInfoAndDeliveriesShowFailedWebhooks — on production-us every
// alert webhook failed ("namespace mismatch: alert_rule_secret") while
// `alerts info` showed only last_fired, and no command listed deliveries.
// info now prints the last delivery and `alerts deliveries` lists the
// ledger. Both accept flags after the alert id.
func TestAlertsInfoAndDeliveriesShowFailedWebhooks(t *testing.T) {
	resetJSONOut(t)
	const id = "0bb97f08c9dd4c338a5e3262d04d8b9d"
	delivery := `[{"id":"d1","rule_id":"` + id + `","status":"failed","attempt_count":1,"last_error":"namespace mismatch: alert_rule_secret","observed_value":12.5,"fired_at":"2026-10-04T23:02:03Z"}]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/apps/h3-wf/alerts/" + id:
			_, _ = w.Write([]byte(`{"id":"` + id + `","name":"errors","enabled":true,"metric":"error_rate_pct","comparison":"gt","threshold":5,"window_spec":"5m","action":"webhook","state":"firing","last_fired_at":"2026-10-04T23:02:03Z"}`))
		case "/v1/apps/h3-wf/alerts/" + id + "/deliveries":
			_, _ = w.Write([]byte(delivery))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	out, restore := captureStdout(t)
	defer restore()

	if code := cmdAlerts([]string{"info", id, "--app", "h3-wf"}); code != 0 {
		t.Fatalf("alerts info exit = %d", code)
	}
	if !strings.Contains(out.String(), "last_delivery: failed at 2026-10-04T23:02:03Z (attempts 1): namespace mismatch: alert_rule_secret") {
		t.Fatalf("alerts info hides the failed delivery:\n%s", out.String())
	}
	if code := cmdAlerts([]string{"deliveries", id, "--app", "h3-wf"}); code != 0 {
		t.Fatalf("alerts deliveries exit = %d", code)
	}
	if !strings.Contains(out.String(), "FIRED_AT") || strings.Count(out.String(), "namespace mismatch: alert_rule_secret") != 2 {
		t.Fatalf("alerts deliveries output:\n%s", out.String())
	}
}
