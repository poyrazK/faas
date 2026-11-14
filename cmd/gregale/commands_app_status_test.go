package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func appStatus(ready api.Condition) api.ResourceStatus {
	ready.Type = api.ConditionReady
	return api.ResourceStatus{Kind: "app", Name: "demo", ObservedAt: "2026-10-30T15:00:00Z", Conditions: []api.Condition{
		ready,
		{Type: api.ConditionProgressing, Status: "False", Reason: "complete", Message: "No deployment is in progress."},
		{Type: api.ConditionDegraded, Status: "False", Reason: "healthy", Message: "fine"},
	}}
}

// statusServer answers GET /v1/apps/demo/status with each status in turn,
// repeating the last, or with 404 when there are none.
func statusServer(t *testing.T, statuses ...api.ResourceStatus) *atomic.Int32 {
	t.Helper()
	var reads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/demo/status" {
			http.NotFound(w, r)
			return
		}
		n := int(reads.Add(1))
		if len(statuses) == 0 {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":404,"code":"not_found","title":"Not found"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(statuses[min(n, len(statuses))-1])
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_status")
	return &reads
}

func captureOutput(t *testing.T) (*bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	oldOut, oldErr := *main.OsStdout, *main.OsStderr
	*main.OsStdout, *main.OsStderr = &stdout, &stderr
	t.Cleanup(func() { *main.OsStdout, *main.OsStderr = oldOut, oldErr })
	return &stdout, &stderr
}

func TestAppStatusPrintsConditions(t *testing.T) {
	statusServer(t, appStatus(api.Condition{Status: "True", Reason: "idle", Message: "No instance is running; the next request starts one."}))
	stdout, _ := captureOutput(t)
	if code := main.Run([]string{"app", "demo", "status"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{"app demo", "Ready", "True", "idle", "Progressing", "gregale app health demo"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}
}

func TestAppWait(t *testing.T) {
	starting := appStatus(api.Condition{Status: "False", Reason: "deploying", Message: "No release is serving yet."})
	ready := appStatus(api.Condition{Status: "True", Reason: "serving", Message: "The app is serving."})
	failed := appStatus(api.Condition{Status: "False", Reason: "not_deployed", Message: "The latest deployment failed.", Terminal: true})
	tests := []struct {
		name      string
		statuses  []api.ResourceStatus
		args      []string
		code      int
		minReads  int32
		wantError string
	}{
		{"holds after a poll", []api.ResourceStatus{starting, ready}, nil, 0, 2, ""},
		{"terminal ends the wait", []api.ResourceStatus{failed}, nil, 1, 1, "will not change without a new request"},
		{"timeout", []api.ResourceStatus{starting}, []string{"--timeout", "1500ms", "--poll-interval", "1s"}, 3, 1, "Ready=True did not hold"},
		{"waits for False", []api.ResourceStatus{ready}, []string{"--for", "Progressing=False"}, 0, 1, ""},
		{"unknown condition type", []api.ResourceStatus{ready}, []string{"--for", "RolledOut"}, 1, 0, "app conditions are Ready, Progressing, Degraded"},
		{"bad status", []api.ResourceStatus{ready}, []string{"--for", "Ready=yes"}, 1, 0, "True, False or Unknown"},
		{"no conditions", nil, nil, 4, 1, "gregale app health demo --wait"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reads := statusServer(t, tc.statuses...)
			_, stderr := captureOutput(t)
			args := append([]string{"app", "demo", "wait", "--poll-interval", "1s"}, tc.args...)
			if code := main.Run(args); code != tc.code {
				t.Errorf("exit = %d, want %d; stderr:\n%s", code, tc.code, stderr)
			}
			if got := reads.Load(); got < tc.minReads {
				t.Errorf("status reads = %d, want at least %d", got, tc.minReads)
			}
			if tc.wantError != "" && !strings.Contains(stderr.String(), tc.wantError) {
				t.Errorf("stderr missing %q:\n%s", tc.wantError, stderr)
			}
		})
	}
}

// A wait that ran out of time reported invalid_request with exit_code 1 in
// its JSON problem while the process exited 3 (CLI journey 2026-11-05,
// #6653). The problem now names the timeout and carries the real exit code;
// an invalid condition stays invalid_request with exit 1.
func TestAppWaitTimeoutJSONMatchesExit(t *testing.T) {
	starting := appStatus(api.Condition{Status: "False", Reason: "deploying", Message: "No release is serving yet."})
	ready := appStatus(api.Condition{Status: "True", Reason: "serving", Message: "The app is serving."})
	for _, tc := range []struct {
		name     string
		status   api.ResourceStatus
		args     []string
		exit     int
		code     string
		category string
	}{
		{"pending condition times out", starting, []string{"--timeout", "1200ms"}, 3, "wait_timed_out", ""},
		{"invalid condition", ready, []string{"--for", "Ready=yes"}, 1, "invalid_request", "invalid_request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			statusServer(t, tc.status)
			_, readStderr, restore := main.SwapIO(t)
			args := append([]string{"--json", "app", "demo", "wait", "--poll-interval", "1s"}, tc.args...)
			code := main.Run(args)
			restore()
			stderr := readStderr()
			if code != tc.exit {
				t.Fatalf("exit = %d, want %d; stderr:\n%s", code, tc.exit, stderr)
			}
			var problem struct {
				Code     string `json:"code"`
				Category string `json:"category"`
				ExitCode int    `json:"exit_code"`
			}
			if err := json.Unmarshal([]byte(stderr), &problem); err != nil {
				t.Fatalf("stderr is not one JSON problem: %v\n%s", err, stderr)
			}
			if problem.Code != tc.code || problem.ExitCode != tc.exit {
				t.Fatalf("problem = %+v, want code %s and exit_code %d", problem, tc.code, tc.exit)
			}
			if tc.category != "" && problem.Category != tc.category {
				t.Fatalf("category = %q, want %q", problem.Category, tc.category)
			}
			if tc.exit == 3 && problem.Category == "invalid_request" {
				t.Fatalf("a timed-out wait is classified as an invalid request: %+v", problem)
			}
		})
	}
}
