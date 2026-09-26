package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// adr: 099 — command crons preserve argv without shell reinterpretation.
func TestCmdCronsAddCommandPreservesArgv(t *testing.T) {
	var got struct {
		AppID          string   `json:"app_id"`
		Schedule       string   `json:"schedule"`
		Path           string   `json:"path"`
		Command        []string `json:"command"`
		CommandShell   bool     `json:"command_shell"`
		TimeoutSeconds int      `json:"timeout_seconds"`
		MaxOutputBytes int      `json:"max_output_bytes"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/crons" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"0123456789abcdef0123456789abcdef","app_id":"my-app","kind":"command","schedule":"0 2 * * *","command":["bin/reindex","--delta"],"timeout_seconds":90,"max_output_bytes":1048576,"enabled":true,"timezone":"UTC","skip_if_running":false,"created_at":"2026-09-25T00:00:00Z"}`))
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	out, _, restore := swapIO(t)
	defer restore()

	code := cmdCrons([]string{
		"add", "--app", "my-app", "--schedule", "0 2 * * *", "--command", "bin/reindex",
		"--arg=--delta", "--timeout-seconds", "90",
	})
	if code != 0 {
		t.Fatalf("crons add command exit = %d; output=%q", code, out.String())
	}
	if got.AppID != "my-app" || got.Schedule != "0 2 * * *" || got.Path != "" ||
		strings.Join(got.Command, "|") != "bin/reindex|--delta" || got.CommandShell ||
		got.TimeoutSeconds != 90 || got.MaxOutputBytes != 0 {
		t.Fatalf("create cron request = %+v", got)
	}
	if !strings.Contains(out.String(), "command [bin/reindex --delta]") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestCmdCronsAddRejectsPathAndCommandBeforeRequest(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	_, errOut, restore := swapIO(t)
	defer restore()

	code := cmdCrons([]string{
		"add", "--app", "my-app", "--schedule", "0 2 * * *", "--path", "/run",
		"--command", "bin/reindex",
	})
	if code == 0 || calls != 0 || !strings.Contains(errOut(), "mutually exclusive") {
		t.Fatalf("exit=%d calls=%d stderr=%q", code, calls, errOut())
	}
}
