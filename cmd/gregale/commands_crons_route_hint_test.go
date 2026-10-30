package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Most failed cron runs in production are apps answering 404 for the cron's
// path. `cron runs` says so and names the path and the fix.
func TestCronsRunsExplainsRouteMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/crons/" + cronsFireNowRunID + "/runs":
			_ = json.NewEncoder(w).Encode(api.ListCronRunsResponse{Runs: []api.CronRun{
				{ID: "run-2", StartedAt: time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC), Outcome: api.CronRunFailed, Attempts: 1,
					Error: `invoke: sched: permanent invoke error: application returned HTTP 404: {"error":"not_found","path":"/healthz"}`},
				{ID: "run-1", StartedAt: time.Date(2026, 10, 29, 8, 0, 0, 0, time.UTC), Outcome: api.CronRunSuccess, Attempts: 1},
			}})
		case "/v1/crons/" + cronsFireNowRunID:
			_ = json.NewEncoder(w).Encode(api.CronResponse{ID: cronsFireNowRunID, Path: "/healthz", Schedule: "0 * * * *"})
		default:
			http.Error(w, "unexpected "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	var out bytes.Buffer
	old := *main.OsStdout
	*main.OsStdout = &out
	code := main.Run([]string{"crons", "runs", cronsFireNowRunID})
	*main.OsStdout = old
	if code != 0 {
		t.Fatalf("crons runs = %d", code)
	}
	for _, want := range []string{"1 of 2 runs failed because the app answered 404 for POST /healthz", "gregale cron update " + cronsFireNowRunID + " --path"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("crons runs output lacks %q:\n%s", want, out.String())
		}
	}
}
