package main_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
