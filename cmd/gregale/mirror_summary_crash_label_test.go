package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// The mirror summary called five deliberate 500 responses "Crashes: 5";
// the count is 5xx or no response (Hunt 12 H12-38).
func TestMirrorSummaryExplainsCrashCount(t *testing.T) {
	main.ResetJSONOut(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/web/mirrors/m-1/summary" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"window_seconds":3600,"total_invocations":11,"crash_count":5}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	out, restore := main.CaptureStdout(t)
	code := main.CmdMirrorSummary([]string{"--app", "web", "--id", "m-1"})
	restore()
	if want := "Crashes:        5 (mirror answered 5xx or not at all)\n"; code != 0 || !strings.Contains(out.String(), want) {
		t.Fatalf("exit %d, want %q in:\n%s", code, want, out.String())
	}
}
