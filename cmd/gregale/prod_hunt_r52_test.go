package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Prod hunt r52: `postgres suspend-timeout set db 30s` printed only usage.
func TestPostgresSuspendTimeoutSaysWhyAValueIsRefused(t *testing.T) {
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	t.Setenv("FAAS_TOKEN", "test-token")
	for _, value := range []string{"30s", "2h", "90.5s"} {
		code, _, stderr := runCLI(t, "postgres", "suspend-timeout", "set", "orders-db", value)
		if code == 0 || !strings.Contains(stderr, `"`+value+`" is not a whole-second duration from 1m0s to 1h0m0s`) {
			t.Fatalf("suspend-timeout %s = %d:\n%s", value, code, stderr)
		}
	}
}

// Prod hunt r52 (rc.381): `app restart --fresh --wait` on an app whose new
// config crashes it at startup printed running/retrying until its 10 min
// timeout. A startup crash repeats on every retry, so the wait stops at the
// first one and says what to do.
func TestRestartWaitStopsOnStartupCrash(t *testing.T) {
	const wakeID = "5f1c7a52-3d0e-4c4b-9a43-2b7f0f6f1a11"
	statusReads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/web/restart":
			_ = json.NewEncoder(w).Encode(api.AppRestartResponse{WakeID: wakeID})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/web/runtime-config-restarts/"+wakeID:
			statusReads++
			_ = json.NewEncoder(w).Encode(api.RuntimeConfigRestartStatusResponse{
				WakeID: wakeID, Status: "retrying", Attempts: 1, FailureReason: api.CodeAppStartupCrash,
				RequestedAt: time.Now().UTC().Add(-time.Minute),
			})
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":404,"code":"not_found","title":"Not found"}`))
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Chdir(t.TempDir())
	code, _, stderr := runCLI(t, "app", "restart", "web", "--fresh", "--wait", "--poll-interval", "1ms", "--timeout", "30s")
	if code == 0 {
		t.Fatalf("restart wait on a startup crash exited 0:\n%s", stderr)
	}
	if statusReads != 1 {
		t.Fatalf("status read %d times, want the wait to stop at the first startup crash", statusReads)
	}
	for _, want := range []string{"Restart is not taking effect", "crashed during startup", "gregale logs", "gregale env rollback", "gregale env unset"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
}
