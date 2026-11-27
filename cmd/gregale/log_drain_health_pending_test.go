package main_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// prod hunt 2026-11-21: a new drain read "unknown (active=false)" and
// "Delivered: 0" for its first minute while its endpoint was already
// receiving lines. Before the first report, health says the counts lag.
func TestLogDrainHealthSaysWhenNoReportArrivedYet(t *testing.T) {
	for _, tc := range []struct {
		updated string
		want    bool
	}{{"", true}, {"2026-11-21T10:16:03Z", false}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"log_drain_id":"d1","status":"unknown","active":false,"updated_at":%q}`, tc.updated)
		}))
		t.Setenv("FAAS_API", srv.URL)
		t.Setenv("FAAS_TOKEN", "fp_test_x")
		code, stdout, stderr := runCLI(t, "logs", "drain", "health", "ef870e67-f974-40f3-977c-2dc889d95c22", "--app", "web")
		srv.Close()
		if code != 0 {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		if got := strings.Contains(stdout, "No delivery report yet"); got != tc.want {
			t.Errorf("updated_at=%q: note shown %v, want %v\n%s", tc.updated, got, tc.want, stdout)
		}
	}
}
