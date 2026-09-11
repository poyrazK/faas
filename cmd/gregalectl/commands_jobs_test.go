package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	testOperatorJobAccountID = "11111111-1111-1111-1111-111111111111"
	testOperatorJobRunID     = "22222222-2222-2222-2222-222222222222"
	testOperatorJobID        = "33333333-3333-3333-3333-333333333333"
	testOperatorJobTraceID   = "4bf92f3577b34da6a3ce929d0e0e4736"
)

func TestJobsCommandsUseAuthenticatedOperatorAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie("faas_sid"); err != nil || cookie.Value != "opaque-session" {
			t.Errorf("session cookie = %v, %v", cookie, err)
		}
		run := api.OperatorJobRun{
			JobID: testOperatorJobID, JobName: "nightly", RunID: testOperatorJobRunID,
			AccountID: testOperatorJobAccountID, AggregateStatus: "running", Tasks: 3,
			TasksRunning: 2, RAMMB: 512, CreatedAt: "2026-09-12T10:00:00Z",
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/ops/jobs/runs":
			if r.URL.Query().Get("account_id") != testOperatorJobAccountID || r.URL.Query().Get("limit") != "20" {
				t.Errorf("active query = %q", r.URL.RawQuery)
			}
			writeTestJSON(w, http.StatusOK, api.OperatorJobRunListResponse{
				AccountID: testOperatorJobAccountID, Runs: []api.OperatorJobRun{run}, Limit: 20, NextOffset: -1,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/ops/jobs/runs/"+testOperatorJobRunID:
			if r.URL.Query().Get("task_limit") != "2" {
				t.Errorf("inspect query = %q", r.URL.RawQuery)
			}
			writeTestJSON(w, http.StatusOK, api.OperatorJobRunDetailResponse{
				Run: run, Tasks: []api.JobTaskResponse{{RunID: testOperatorJobRunID, TaskIndex: 0, Status: "claimed", Attempt: 1}}, TaskLimit: 2, NextTaskOffset: -1,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/ops/jobs/runs/"+testOperatorJobRunID+"/cancel":
			if r.URL.Query().Get("confirm") != "true" || r.URL.Query().Get("reason") != "jobs_queue_incident" {
				t.Errorf("cancel query = %q", r.URL.RawQuery)
			}
			if r.Header.Get("Idempotency-Key") == "" || r.Header.Get(operatorTraceIDHeader) != testOperatorJobTraceID {
				t.Errorf("cancel safety headers = %#v", r.Header)
			}
			run.AggregateStatus = "cancelled"
			writeTestJSON(w, http.StatusOK, api.OperatorJobRunCancelResponse{
				Run: run, CancelledAt: "2026-09-12T10:01:00Z", Reason: "jobs_queue_incident",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	installTestOperatorSession(t, server.URL, "opaque-session")
	out, stderr, restore := captureOperatorIO()
	defer restore()

	commands := []struct {
		args []string
		want string
	}{
		{args: []string{"active", "--account-id", testOperatorJobAccountID, "--limit", "20"}, want: "active_runs=1"},
		{args: []string{"inspect", "--run-id", testOperatorJobRunID, "--task-limit", "2"}, want: "task index=0"},
		{args: []string{"cancel", "--run-id", testOperatorJobRunID, "--reason", "jobs_queue_incident", "--trace-id", testOperatorJobTraceID, "--yes"}, want: "trace_id=" + testOperatorJobTraceID},
	}
	for _, command := range commands {
		out.Reset()
		stderr.Reset()
		if code := cmdJobsDispatch(command.args); code != 0 {
			t.Fatalf("%v exit=%d stderr=%s", command.args, code, stderr.String())
		}
		if !strings.Contains(out.String(), command.want) {
			t.Fatalf("%v stdout=%q, want %q", command.args, out.String(), command.want)
		}
	}
}

func TestJobsCancelRequiresSafetyInputs(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"cancel", "--run-id", testOperatorJobRunID, "--yes"}, want: "--reason is required"},
		{args: []string{"cancel", "--run-id", testOperatorJobRunID, "--reason", "incident_123"}, want: "--yes required"},
		{args: []string{"cancel", "--run-id", testOperatorJobRunID, "--reason", "incident_123", "--trace-id", "bad", "--yes"}, want: "32 lowercase hex"},
	} {
		_, stderr, restore := captureOperatorIO()
		if code := cmdJobsDispatch(tc.args); code != 2 {
			restore()
			t.Fatalf("%v exit=%d", tc.args, code)
		}
		if !strings.Contains(stderr.String(), tc.want) {
			restore()
			t.Fatalf("%v stderr=%q, want %q", tc.args, stderr.String(), tc.want)
		}
		restore()
	}
}
