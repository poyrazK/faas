package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// The route advice header printed nanosecond timestamps, like analytics
// before H12-23 (Hunt 12 H12-23 follow-up).
func TestRoutesAdviseWindowPrintsSeconds(t *testing.T) {
	main.ResetJSONOut(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/web/routes/advice" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"slug":"web","from":"2026-10-15T17:06:22.030575282Z","until":"2026-10-22T17:06:22.030575282Z","cache_max_age_seconds":60}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	out, restore := main.CaptureStdout(t)
	code := main.Run([]string{"routes", "advise", "web"})
	restore()
	if want := "Route advice for web — traffic 2026-10-15 17:06:22 UTC to 2026-10-22 17:06:22 UTC, 60s cache what-if"; code != 0 || !strings.Contains(out.String(), want) {
		t.Fatalf("exit %d, want %q in:\n%s", code, want, out.String())
	}
}
