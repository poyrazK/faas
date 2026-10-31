package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// `usage daily` printed unsorted rows with no header, so the requests
// column was unlabelled and there was no total (Hunt 12 H12-41).
func TestUsageDailyHeaderOrderAndTotal(t *testing.T) {
	main.ResetJSONOut(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps":
			_, _ = w.Write([]byte(`[{"id":"a-1","slug":"small"},{"id":"a-2","slug":"big"}]`))
		case "/v1/usage/daily":
			_, _ = w.Write([]byte(`{"items":[{"app_id":"a-1","day":"2026-10-22","mb_seconds":3600000,"requests":10},{"app_id":"a-2","day":"2026-10-22","mb_seconds":36000000,"requests":4}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	out, restore := main.CaptureStdout(t)
	code := main.CmdUsageDaily([]string{"--day", "2026-10-22"})
	restore()
	got := out.String()
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if code != 0 || len(lines) != 4 {
		t.Fatalf("exit %d, want header, 2 rows, total:\n%s", code, got)
	}
	if !strings.HasPrefix(lines[0], "APP") || !strings.Contains(lines[0], "REQUESTS") {
		t.Errorf("header = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "big ") || !strings.HasPrefix(lines[2], "small ") {
		t.Errorf("rows not largest first:\n%s", got)
	}
	if lines[3] != "Total: 11.000 GB-hours across 2 apps" {
		t.Errorf("total = %q", lines[3])
	}
}
