package main_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

const cronOneOffTaskID = "4f1c2a7e-0b9d-4c8e-9a51-2f6d3e8b7c10"

// ADR-20261026145832: cron owns every schedule, so a one-off request is
// created, listed, read and cancelled with the cron verbs; the work is a
// delayed task.
func TestCronsOneOffRequestsUseDelayedTasks(t *testing.T) {
	var requests []string
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		w.Header().Set("Content-Type", "application/json")
		task := fmt.Sprintf(`{"id":%q,"app_slug":"demo","state":"pending","scheduled_at":"2030-01-01T09:00:00Z","method":"POST","path":"/report"}`, cronOneOffTaskID)
		switch {
		case r.URL.Path == "/v1/apps/demo/delayed-tasks" && r.Method == http.MethodGet:
			_, _ = fmt.Fprintf(w, `{"tasks":[%s]}`, task)
		case r.Method == http.MethodDelete:
			_, _ = fmt.Fprint(w, strings.Replace(task, `"pending"`, `"cancelled"`, 1))
		case strings.HasPrefix(r.URL.Path, "/v1/apps/demo/delayed-tasks") || strings.HasPrefix(r.URL.Path, "/v1/delayed-tasks/"):
			_, _ = fmt.Fprint(w, task)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(w, `{"title":"not found","status":404,"code":"not_found"}`)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")

	for _, tc := range []struct {
		args []string
		want string
		body string
	}{
		{[]string{"crons", "add", "--app", "demo", "--at", "2030-01-01T09:00:00Z", "--path", "/report"}, "POST /v1/apps/demo/delayed-tasks", "2030-01-01T09:00:00Z"},
		{[]string{"crons", "add", "--app", "demo", "--in=30m"}, "POST /v1/apps/demo/delayed-tasks", ""},
		{[]string{"crons", "list", "--app", "demo", "--one-off"}, "GET /v1/apps/demo/delayed-tasks", ""},
		{[]string{"crons", "info", "--one-off", cronOneOffTaskID}, "GET /v1/delayed-tasks/" + cronOneOffTaskID, ""},
		{[]string{"crons", "cancel", cronOneOffTaskID}, "DELETE /v1/delayed-tasks/" + cronOneOffTaskID, ""},
	} {
		requests, bodies = nil, nil
		code, stdout, stderr := runCLI(t, tc.args...)
		if code != 0 || len(requests) == 0 || requests[len(requests)-1] != tc.want {
			t.Errorf("%v = %d, requests %v, want last %q\n%s%s", tc.args, code, requests, tc.want, stdout, stderr)
			continue
		}
		if tc.body != "" && !strings.Contains(bodies[len(bodies)-1], tc.body) {
			t.Errorf("%v sent %s, want it to carry %s", tc.args, bodies[len(bodies)-1], tc.body)
		}
	}

	requests = nil
	code, _, stderr := runCLI(t, "crons", "add", "--app", "demo", "--at", "2030-01-01T09:00:00Z", "--schedule", "*/5 * * * *")
	if code == 0 || !strings.Contains(stderr, "recurring cron") || len(requests) != 0 {
		t.Errorf("--at with --schedule = %d %q, requests %v; want a local usage error", code, stderr, requests)
	}
}

// With delayed execution hidden for the account, the one-off forms answer
// like unknown flags, as the delayed-task command itself does
// (ADR-20261023115741).
func TestCronsOneOffFollowsDelayedExecutionVisibility(t *testing.T) {
	api := &capabilitiesAPI{}
	server := httptest.NewServer(api)
	defer server.Close()
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "")
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("FAAS_COMPLETION_CACHE_PATH", filepath.Join(t.TempDir(), "cache.json"))
	api.setHidden("delayed-execution")
	_, _, _ = runCLI(t, "help", "--all") // caches the account view

	for _, tc := range []struct {
		args []string
		flag string
	}{
		{[]string{"crons", "add", "--app", "demo", "--at", "2030-01-01T09:00:00Z"}, "--at"},
		{[]string{"crons", "list", "--app", "demo", "--one-off"}, "--one-off"},
		{[]string{"crons", "info", "--one-off", cronOneOffTaskID}, "--one-off"},
	} {
		if code, _, stderr := runCLI(t, tc.args...); code == 0 || !strings.Contains(stderr, "unknown flag "+tc.flag) {
			t.Errorf("%v with delayed execution hidden = %d %q, want unknown flag %s", tc.args, code, stderr, tc.flag)
		}
	}
	// One id is then a usage error of the cron-run cancel (printed to the
	// process stderr), not a delayed-task cancellation.
	if code, stdout, _ := runCLI(t, "crons", "cancel", cronOneOffTaskID); code == 0 || strings.Contains(stdout, "cancelled") {
		t.Errorf("one-id cancel with delayed execution hidden = %d %q, want a usage error", code, stdout)
	}
}
