package main_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// TestCmdLogsParkedInstanceWithoutOutput is Hunt 14 H14-62: a scale-to-zero
// app's newest instance was restored from a snapshot and printed nothing, and
// `gregale logs` blamed an archive gap and retention.
func TestCmdLogsParkedInstanceWithoutOutput(t *testing.T) {
	main.ResetJSONOut(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/instances"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `[{"id":"inst-restored","state":"parked","started_at":"2026-10-26T01:38:54Z","parked_at":"2026-10-26T01:38:55Z"}]`)
		case r.URL.Query().Get("archive_instance") != "" || r.URL.Query().Get("instance") != "" || strings.Contains(r.URL.Path, "archive"):
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: end\ndata: {\"reason\":\"archive_missing\"}\n\n")
		default:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: degraded\ndata: {\"code\":\"not_found\"}\n\n")
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	_, restoreOut := main.CaptureStdout(t)
	stderr, restoreErr := main.CaptureStderr(t)
	_ = main.CmdLogs([]string{"myapp"})
	restoreErr()
	restoreOut()
	got := stderr.String()
	if !strings.Contains(got, "No retained output from instance inst-restored: it printed nothing while it ran.") ||
		!strings.Contains(got, "does not repeat the startup output") {
		t.Fatalf("stderr = %q, want the no-output explanation", got)
	}
	if strings.Contains(got, "archive gap") || strings.Contains(got, "archive retention") {
		t.Fatalf("stderr = %q, still blames an archive gap", got)
	}
}
