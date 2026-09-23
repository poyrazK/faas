package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const appTaskCLIReceipt = `{"id":"2bdd4251-f567-4a48-9f66-a155bbfa7751","app_id":"0123456789abcdef0123456789abcdef","deployment_id":"abcdef0123456789abcdef0123456789","deployment_scope":"default","kind":"manual","command":["bin/task","--compact"],"command_shell":false,"status":"queued","timeout_seconds":30,"max_output_bytes":2048,"output_truncated":false,"created_at":"2026-09-23T00:00:00Z","updated_at":"2026-09-23T00:00:00Z"}`

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
