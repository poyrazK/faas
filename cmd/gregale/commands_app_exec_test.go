package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

const appTaskCLIReceipt = `{"id":"2bdd4251-f567-4a48-9f66-a155bbfa7751","app_id":"0123456789abcdef0123456789abcdef","deployment_id":"abcdef0123456789abcdef0123456789","deployment_scope":"default","kind":"manual","command":["bin/task","--compact"],"command_shell":false,"status":"queued","timeout_seconds":30,"max_output_bytes":2048,"output_truncated":false,"created_at":"2026-09-23T00:00:00Z","updated_at":"2026-09-23T00:00:00Z"}`

func TestRunAppExecPreservesArgumentsAfterTerminator(t *testing.T) {
	for _, remoteFlag := range []string{"--help", "-h", "--json", "-j", "--json=custom"} {
		for _, useJSON := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%t", remoteFlag, useJSON), func(t *testing.T) {
				resetJSONOut(t)
				t.Setenv("FAAS_JSON", "0")
				t.Setenv("XDG_CONFIG_HOME", t.TempDir())
				// macOS ignores XDG_CONFIG_HOME; HOME isolates os.UserConfigDir there.
				t.Setenv("HOME", os.Getenv("XDG_CONFIG_HOME"))
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.Method != http.MethodPost || r.URL.Path != "/v1/apps/my-app/tasks" {
						t.Errorf("request = %s %s", r.Method, r.URL.Path)
					}
					var request api.CreateAppTaskRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Errorf("decode request: %v", err)
					}
					if got := strings.Join(request.Command, "|"); got != "bin/task|"+remoteFlag {
						t.Errorf("remote command = %q", got)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(appTaskCLIReceipt))
				}))
				defer server.Close()
				t.Setenv("FAAS_API", server.URL)
				t.Setenv("FAAS_TOKEN", "test-token")
				out, stderr, restore := swapIO(t)
				defer restore()
				args := []string{"app", "my-app", "exec", "--detach", "--", "bin/task", remoteFlag}
				if useJSON {
					args = append([]string{"--json"}, args...)
				}
				if code := run(args); code != 0 || requests.Load() != 1 {
					t.Fatalf("exit=%d requests=%d stdout=%s stderr=%s", code, requests.Load(), out.String(), stderr())
				}
				if useJSON {
					var receipt api.AppTaskResponse
					if err := json.Unmarshal(out.Bytes(), &receipt); err != nil || receipt.ID == "" {
						t.Fatalf("invalid JSON receipt: %v: %s", err, out.String())
					}
				} else if !strings.Contains(out.String(), "queued for my-app") {
					t.Fatalf("human receipt = %s", out.String())
				}
			})
		}
	}
}

func TestCmdAppExecPreservesIdempotencyKeyOnRetry(t *testing.T) {
	resetJSONOut(t)
	var submissions atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		submissions.Add(1)
		if got := r.Header.Get("Idempotency-Key"); got != "stable-task-submit" {
			t.Errorf("Idempotency-Key = %q, want stable-task-submit", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(appTaskCLIReceipt))
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	_, _, restore := swapIO(t)
	defer restore()
	for attempt := 0; attempt < 2; attempt++ {
		if code := cmdAppExec("my-app", []string{"--detach", "--idempotency-key", "stable-task-submit", "--", "bin/task"}); code != 0 {
			t.Fatalf("attempt %d exit = %d", attempt, code)
		}
	}
	if submissions.Load() != 2 {
		t.Fatalf("submissions = %d, want 2", submissions.Load())
	}
}

func TestCmdAppExecDetachedSubmitsArgv(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/apps/my-app/tasks" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var request struct {
			Command        []string `json:"command"`
			CommandShell   bool     `json:"command_shell"`
			TimeoutSeconds int      `json:"timeout_seconds"`
			MaxOutputBytes int      `json:"max_output_bytes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if strings.Join(request.Command, "|") != "bin/task|--compact" || request.CommandShell || request.TimeoutSeconds != 30 || request.MaxOutputBytes != 2048 {
			t.Fatalf("request = %+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(appTaskCLIReceipt))
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	out, _, restore := swapIO(t)
	defer restore()

	code := cmdAppDispatch([]string{"my-app", "exec", "--detach", "--timeout-seconds", "30", "--max-output-bytes", "2048", "--", "bin/task", "--compact"})
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out.String(), "queued for my-app") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestCmdAppExecWaitsAndPropagatesRemoteExit(t *testing.T) {
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(appTaskCLIReceipt))
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/my-app/tasks/2bdd4251-f567-4a48-9f66-a155bbfa7751" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		polls.Add(1)
		_, _ = w.Write([]byte(`{"id":"2bdd4251-f567-4a48-9f66-a155bbfa7751","app_id":"0123456789abcdef0123456789abcdef","deployment_id":"abcdef0123456789abcdef0123456789","deployment_scope":"default","kind":"manual","command":["bin/task"],"command_shell":false,"status":"failed","timeout_seconds":30,"max_output_bytes":2048,"stdout_tail":"partial output\n","stderr_tail":"bad input\n","output_truncated":true,"exit_code":17,"failure":{"code":"process_exit","message":"command exited unsuccessfully"},"created_at":"2026-09-23T00:00:00Z","updated_at":"2026-09-23T00:00:01Z","finished_at":"2026-09-23T00:00:01Z"}`))
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	out, errOut, restore := swapIO(t)
	defer restore()

	code := cmdAppExec("my-app", []string{"--poll-interval", "1ms", "--wait-timeout", "1s", "--", "bin/task"})
	if code != 17 {
		t.Fatalf("exit = %d, want remote exit 17", code)
	}
	if polls.Load() == 0 || !strings.Contains(out.String(), "partial output") {
		t.Fatalf("polls=%d stdout=%q", polls.Load(), out.String())
	}
	stderr := errOut()
	for _, want := range []string{"status=failed", "bad input", "output was truncated", "process_exit"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q: %q", want, stderr)
		}
	}
}

func TestCmdAppExecCanWaitThroughManagedOperation(t *testing.T) {
	const operationID = "45f93131-d589-4f4b-a573-3dd0d8f4cf61"
	const taskID = "2bdd4251-f567-4a48-9f66-a155bbfa7751"
	var polled atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/my-app/operations/tasks":
			if r.Header.Get("Idempotency-Key") != "stable-submit" {
				t.Errorf("Idempotency-Key = %q", r.Header.Get("Idempotency-Key"))
			}
			var request struct {
				Policy         string          `json:"policy"`
				Key            json.RawMessage `json:"key"`
				EquivalenceKey string          `json:"equivalence_key"`
				Task           struct {
					Command []string `json:"command"`
				} `json:"task"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode request: %v", err)
			}
			if request.Policy != "customer-sync" || string(request.Key) != `"customer:acme:sync"` || request.EquivalenceKey != "sync-request-7" || strings.Join(request.Task.Command, " ") != "bin/sync --full" {
				t.Errorf("managed task request = %+v", request)
			}
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"` + operationID + `","joined":false,"status_url":"/v1/operations/` + operationID + `"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/operations/"+operationID:
			polled.Add(1)
			_, _ = w.Write([]byte(`{"id":"` + operationID + `","state":"completed","generation":1,"result":{"app_task_id":"` + taskID + `"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/my-app/tasks/"+taskID:
			_, _ = w.Write([]byte(`{"id":"` + taskID + `","app_id":"0123456789abcdef0123456789abcdef","deployment_id":"abcdef0123456789abcdef0123456789","deployment_scope":"default","kind":"manual","command":["bin/sync","--full"],"command_shell":false,"status":"succeeded","timeout_seconds":30,"max_output_bytes":2048,"stdout_tail":"sync complete\n","stderr_tail":"","output_truncated":false,"exit_code":0,"created_at":"2026-09-23T00:00:00Z","updated_at":"2026-09-23T00:00:01Z","finished_at":"2026-09-23T00:00:01Z"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	out, _, restore := swapIO(t)
	defer restore()

	code := cmdAppExec("my-app", []string{
		"--operation-policy", "customer-sync", "--operation-key", `"customer:acme:sync"`,
		"--equivalence-key", "sync-request-7", "--idempotency-key", "stable-submit",
		"--poll-interval", "1ms", "--wait-timeout", "1s", "--", "bin/sync", "--full",
	})
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if polled.Load() == 0 || !strings.Contains(out.String(), "sync complete") {
		t.Fatalf("polls=%d stdout=%q", polled.Load(), out.String())
	}
}

func TestCmdAppExecShellRequiresOneCommandString(t *testing.T) {
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	t.Setenv("FAAS_TOKEN", "test-token")
	_, errOut, restore := swapIO(t)
	defer restore()
	if code := cmdAppExec("my-app", []string{"--shell", "--", "echo", "hello"}); code == 0 {
		t.Fatal("multi-argument shell command unexpectedly succeeded")
	}
	if !strings.Contains(errOut(), "usage: gregale app <slug> exec") {
		t.Fatalf("stderr = %q", errOut())
	}
}
