package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

const appTaskFinishedReceipt = `{"id":"2bdd4251-f567-4a48-9f66-a155bbfa7751","app_id":"0123456789abcdef0123456789abcdef","deployment_id":"abcdef0123456789abcdef0123456789","deployment_scope":"default","kind":"manual","command":["bin/migrate","--to","head"],"command_shell":false,"status":"failed","timeout_seconds":30,"max_output_bytes":2048,"stdout_tail":"applied 3\n","stderr_tail":"boom\n","output_truncated":false,"exit_code":2,"failure":{"code":"process_exit","message":"command exited unsuccessfully"},"started_at":"2026-09-23T00:00:00Z","finished_at":"2026-09-23T00:00:01.5Z","created_at":"2026-09-23T00:00:00Z","updated_at":"2026-09-23T00:00:02Z"}`

func appTasksTestServer(t *testing.T, handle func(w http.ResponseWriter, r *http.Request)) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		handle(w, r)
	}))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
}

func TestCmdAppTasksListPrintsHistory(t *testing.T) {
	appTasksTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/my-app/tasks" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("limit"); got != "1" {
			t.Fatalf("limit = %q, want 1", got)
		}
		_, _ = w.Write([]byte(`{"tasks":[` + appTaskFinishedReceipt + `],"limit":1,"offset":0,"next_offset":1}`))
	})
	out, errOut, restore := main.SwapIO(t)
	defer restore()

	if code := main.CmdAppDispatch([]string{"my-app", "tasks", "--limit", "1"}); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut())
	}
	for _, want := range []string{"2bdd4251-f567-4a48-9f66-a155bbfa7751", "failed", "manual", "1.5s", "bin/migrate --to head"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stdout missing %q:\n%s", want, out.String())
		}
	}
	if !strings.Contains(errOut(), "tasks my-app --offset 1") {
		t.Errorf("stderr should name the next page: %q", errOut())
	}
}

func TestCmdAppTasksShowPrintsOutputTails(t *testing.T) {
	appTasksTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/my-app/tasks/2bdd4251-f567-4a48-9f66-a155bbfa7751" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(appTaskFinishedReceipt))
	})
	out, errOut, restore := main.SwapIO(t)
	defer restore()

	if code := main.CmdAppTasks("my-app", []string{"show", "2bdd4251-f567-4a48-9f66-a155bbfa7751"}); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut())
	}
	for _, want := range []string{"Exit code:", "2", "process_exit", "--- stdout ---", "applied 3", "--- stderr ---", "boom"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stdout missing %q:\n%s", want, out.String())
		}
	}
}

func TestCmdAppTasksCancelRunningTask(t *testing.T) {
	appTasksTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost && r.Method != http.MethodDelete {
			t.Fatalf("method = %s", r.Method)
		}
		if !strings.HasPrefix(r.URL.Path, "/v1/apps/my-app/tasks/2bdd4251-f567-4a48-9f66-a155bbfa7751") {
			t.Fatalf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(strings.Replace(appTaskCLIReceipt, `"status":"queued"`, `"status":"running","cancel_requested_at":"2026-09-23T00:00:03Z"`, 1)))
	})
	out, errOut, restore := main.SwapIO(t)
	defer restore()

	if code := main.CmdAppTasks("my-app", []string{"cancel", "2bdd4251-f567-4a48-9f66-a155bbfa7751"}); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut())
	}
	if !strings.Contains(out.String(), "Cancellation requested") || !strings.Contains(out.String(), "tasks show my-app 2bdd4251") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestCmdAppTasksRejectsBadIDBeforeCallingTheAPI(t *testing.T) {
	appTasksTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
	})
	_, errOut, restore := main.SwapIO(t)
	defer restore()

	if code := main.CmdAppTasks("my-app", []string{"show", "not-a-task"}); code == 0 {
		t.Fatal("exit = 0 for an invalid task ID")
	}
	if !strings.Contains(errOut(), "gregale app tasks my-app") {
		t.Fatalf("stderr should say how to find task IDs: %q", errOut())
	}
}
