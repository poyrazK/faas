package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// cronCLIServer answers the manual-run POST and the request poll with the
// given terminal status, and counts every request it sees.
func cronCLIServer(t *testing.T, status string, calls *atomic.Int32) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/crons/"+cronsFireNowRunID+"/run":
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(api.FireCronResponse{RequestID: cronsFireNowRequestID, CronID: cronsFireNowRunID, Status: "pending"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/cron-fire-now-requests/"+cronsFireNowRequestID:
			resp := makeFireNowResponse()
			resp.Status = status
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
}

// stderrOf runs the CLI with os.Stderr captured.
func stderrOf(t *testing.T, args ...string) (int, string) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	old, oldPkg := os.Stderr, *main.OsStderr
	os.Stderr, *main.OsStderr = f, f
	code := main.Run(args)
	os.Stderr, *main.OsStderr = old, oldPkg
	_ = f.Close()
	out, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return code, string(out)
}

// `cron run --wait` follows the manual run: a script sees a failed run as a
// non-zero exit instead of the 0 that only means "queued".
func TestCronsRunWaitExitsOnTheRunOutcome(t *testing.T) {
	for status, want := range map[string]int{"succeeded": 0, "failed": 1, "cancelled": 1} {
		t.Run(status, func(t *testing.T) {
			var calls atomic.Int32
			cronCLIServer(t, status, &calls)
			if code := main.Run([]string{"crons", "run", cronsFireNowRunID, "--wait", "--timeout", "30s"}); code != want {
				t.Fatalf("crons run --wait on a %s run = %d, want %d", status, code, want)
			}
			if calls.Load() < 2 {
				t.Fatalf("crons run --wait made %d requests, want the POST and at least one poll", calls.Load())
			}
		})
	}
	t.Run("timeout_requires_wait", func(t *testing.T) {
		var calls atomic.Int32
		cronCLIServer(t, "succeeded", &calls)
		if code := main.Run([]string{"crons", "run", cronsFireNowRunID, "--timeout", "1m"}); code != 1 || calls.Load() != 0 {
			t.Fatalf("--timeout without --wait: exit %d after %d requests, want 1 with no request", code, calls.Load())
		}
	})
}

// Cron schedules are not triggers (issue #2757): the triggers commands refuse
// a cron filter and the former cron flags locally and name the cron commands.
func TestTriggersCommandsPointCronsAtTheCronCommands(t *testing.T) {
	for _, args := range [][]string{
		{"triggers", "list", "--kind", "cron"},
		{"triggers", "list", "--kind=cron"},
		{"triggers", "update", "0123456789abcdef0123456789abcdef", "--schedule", "* * * * *"},
		{"triggers", "update", "0123456789abcdef0123456789abcdef", "--path=/x"},
	} {
		var calls atomic.Int32
		cronCLIServer(t, "succeeded", &calls)
		code, stderr := stderrOf(t, args...)
		if code != 1 || calls.Load() != 0 {
			t.Fatalf("%v: exit %d after %d requests, want 1 with no request", args, code, calls.Load())
		}
		if !strings.Contains(stderr, "gregale cron list") {
			t.Fatalf("%v: stderr does not point at the cron commands:\n%s", args, stderr)
		}
	}
}
