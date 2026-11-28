package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// In a linked checkout the task ID alone is enough, verb first.
func TestCmdAppTasksShowInLinkedCheckoutTakesTheTaskID(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := main.SaveProjectContext(root, main.LocalProjectContext{Version: main.ProjectContextVersion, Project: "shop", App: "demo"}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	var paths []string
	appTasksTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_, _ = w.Write([]byte(appTaskFinishedReceipt))
	})
	_, errOut, restore := main.SwapIO(t)
	defer restore()

	if code := main.Run([]string{"app", "tasks", "show", "2bdd4251-f567-4a48-9f66-a155bbfa7751"}); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut())
	}
	if len(paths) != 1 || paths[0] != "/v1/apps/demo/tasks/2bdd4251-f567-4a48-9f66-a155bbfa7751" {
		t.Fatalf("requests = %v, want the linked app's task", paths)
	}
}

// retry re-submits the finished task's command, shell mode, timeout and
// output limit as a new task.
func TestCmdAppTasksRetryResubmitsTheCommand(t *testing.T) {
	var created map[string]any
	appTasksTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/my-app/tasks/2bdd4251-f567-4a48-9f66-a155bbfa7751":
			_, _ = w.Write([]byte(appTaskFinishedReceipt))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/my-app/tasks":
			if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
				t.Fatal(err)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(strings.Replace(strings.Replace(appTaskFinishedReceipt, "2bdd4251-f567-4a48-9f66-a155bbfa7751", "6f0e3c1a-9a5b-4c4e-8d2f-0a1b2c3d4e5f", 1), `"status":"failed"`, `"status":"queued"`, 1)))
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
	})
	out, errOut, restore := main.SwapIO(t)
	defer restore()

	if code := main.CmdAppTasks("my-app", []string{"retry", "2bdd4251-f567-4a48-9f66-a155bbfa7751"}); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut())
	}
	command, _ := json.Marshal(created["command"])
	if string(command) != `["bin/migrate","--to","head"]` || created["timeout_seconds"] != float64(30) || created["max_output_bytes"] != float64(2048) {
		t.Fatalf("retry request = %v", created)
	}
	if !strings.Contains(out.String(), "6f0e3c1a-9a5b-4c4e-8d2f-0a1b2c3d4e5f") || !strings.Contains(out.String(), "tasks show my-app 6f0e3c1a") {
		t.Fatalf("stdout should name the new task and how to follow it:\n%s", out.String())
	}
}

func TestCmdAppTasksRetryRefusesWhatItCannotReproduce(t *testing.T) {
	for name, tc := range map[string]struct{ from, to, want string }{
		"still running": {`"status":"failed"`, `"status":"running"`, "still running"},
		"release":       {`"kind":"manual"`, `"kind":"release"`, "release task"},
		"interactive":   {`"status":"failed"`, `"status":"failed","interactive":true`, "app exec -it"},
		"platform":      {`"command":["bin/migrate","--to","head"]`, `"command":["__gregale_port_forward_v1__","5432"]`, "platform command"},
	} {
		t.Run(name, func(t *testing.T) {
			receipt := strings.Replace(appTaskFinishedReceipt, tc.from, tc.to, 1)
			appTasksTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Fatalf("a refused retry must not submit: %s %s", r.Method, r.URL.Path)
				}
				_, _ = w.Write([]byte(receipt))
			})
			_, errOut, restore := main.SwapIO(t)
			defer restore()
			if code := main.CmdAppTasks("my-app", []string{"retry", "2bdd4251-f567-4a48-9f66-a155bbfa7751"}); code == 0 {
				t.Fatal("retry succeeded")
			}
			if !strings.Contains(errOut(), tc.want) {
				t.Fatalf("stderr = %q, want %q", errOut(), tc.want)
			}
		})
	}
}
