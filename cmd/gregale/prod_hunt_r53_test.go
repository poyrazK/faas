package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// linkedAppServer records request paths and answers every call with an
// empty JSON object, enough for commands that only need to reach the API.
func linkedAppServer(t *testing.T, respond func(w http.ResponseWriter, r *http.Request) bool) *[]string {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if respond != nil && respond(w, r) {
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "test: unhandled"))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	dir := t.TempDir()
	linkCheckoutTo(t, dir, "web")
	t.Chdir(dir)
	return &paths
}

// Prod hunt r53 (rc.381): `cron create --in 1m --path /once` in the app's
// checkout printed `delayed-task add --app` usage; the recurring form used
// the linked app.
func TestOneOffCronUsesTheLinkedApp(t *testing.T) {
	paths := linkedAppServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/apps/web/delayed-tasks" {
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.DelayedTaskResponse{ID: "fe2f184b-d708-4dec-93e8-91457c22aa9e", ScheduledAt: time.Now().Add(time.Minute)})
			return true
		}
		return false
	})
	if code, _, stderr := runCLI(t, "cron", "create", "--in", "1m", "--path", "/once"); code != 0 {
		t.Fatalf("cron create --in = %d:\n%s\nrequests: %q", code, stderr, *paths)
	}
}

// Without a link the one-off form says --app is missing instead of only
// printing another command's usage.
func TestOneOffCronWithoutAppSaysSo(t *testing.T) {
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Chdir(t.TempDir())
	code, _, stderr := runCLI(t, "cron", "create", "--in", "1m", "--path", "/once")
	if code == 0 || !strings.Contains(stderr, "App required") || !strings.Contains(stderr, "--app <slug>") {
		t.Fatalf("cron create --in without app = %d:\n%s", code, stderr)
	}
}

// Prod hunt r53: `release verify <id>`, the check every deploy prints,
// printed usage in the app's checkout without --app.
func TestReleaseVerifyUsesTheLinkedApp(t *testing.T) {
	paths := linkedAppServer(t, nil)
	_, _, stderr := runCLI(t, "release", "verify", "cc1f2bbd-50c2-48ae-a4cd-e14a5f1a24cc")
	if strings.Contains(stderr, "usage:") || len(*paths) == 0 {
		t.Fatalf("release verify did not take the linked app:\n%s\nrequests: %q", stderr, *paths)
	}
}
