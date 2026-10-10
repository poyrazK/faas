package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the public dispatcher and decode its entire output, so a human
// heading or trailing status message cannot silently break automation.
func TestJSONCommandReceipts(t *testing.T) {
	for _, tc := range []struct {
		args                          []string
		method, path, response, field string
		want                          any
	}{
		{[]string{"delayed-task", "get", "0123456789abcdef0123456789abcdef"}, "GET", "/v1/delayed-tasks/0123456789abcdef0123456789abcdef", `{"id":"task-json","state":"pending"}`, "id", "task-json"},
		{[]string{"dlq", "list", "demo"}, "GET", "/v1/apps/demo/dlq", `{"app_slug":"demo","events":[]}`, "app_slug", "demo"},
		{[]string{"invoices"}, "GET", "/v1/invoices", `{"items":[],"next_before":"invoice-next"}`, "next_before", "invoice-next"},
		{[]string{"invitations", "peek", "invite-token"}, "GET", "/v1/invitations/invite-token", `{"email":"cli@example.test","status":"pending"}`, "email", "cli@example.test"},
		{[]string{"issues", "list", "--app", "demo"}, "GET", "/v1/apps/demo/issues", `{"items":[],"next_cursor":"issue-next"}`, "next_cursor", "issue-next"},
		{[]string{"platform-tenants", "list"}, "GET", "/v1/account/platform-tenants", `{"tenants":[],"next_page_token":"tenant-next"}`, "next_page_token", "tenant-next"},
		{[]string{"tenant-surfaces", "list", "--app", "demo"}, "GET", "/v1/apps/demo/tenant-surfaces", `{"surfaces":[{"id":"surface-json","name":"customer"}]}`, "id", "surface-json"},
		{[]string{"throttle-suggestions", "demo"}, "GET", "/v1/apps/demo/throttle-suggestions", `{"app_id":"app-json","suggestions":[]}`, "app_id", "app-json"},
		{[]string{"triggers", "get", "trigger-json"}, "GET", "/v1/triggers/trigger-json", `{"id":"trigger-json","kind":"cron"}`, "id", "trigger-json"},
		{[]string{"github", "bind", "demo", "--installation-id", "42", "--repo", "example/demo"}, "POST", "/v1/apps/demo/github/bind", `{"binding_id":"binding-json","repo_full_name":"example/demo"}`, "binding_id", "binding-json"},
		{[]string{"deliver", "demo", "hook-json", "--type", "invoice.created", "--data", "{}"}, "POST", "/v1/apps/demo/outbox", `{"id":"delivery-json","status":"pending"}`, "id", "delivery-json"},
		{[]string{"rollback", "demo"}, "POST", "/v1/apps/demo/rollback", `{"id":"deployment-json","status":"live"}`, "id", "deployment-json"},
		{[]string{"send", "demo", "--type", "invoice.created", "--data", "{}"}, "POST", "/v1/apps/demo/inbox", `{"id":"message-json","status":"pending"}`, "id", "message-json"},
		{[]string{"registry", "list", "--app", "demo"}, "GET", "/v1/apps/demo/registry-credentials", `{"credentials":[],"count":0,"quota_max":3}`, "quota_max", float64(3)},
		{[]string{"realtime", "publish", "demo", "endpoint-1", "room-a", "--data", "hello"}, "POST", "/v1/apps/demo/realtime/endpoints/endpoint-1/channels/room-a/publish", `{"queued":3}`, "operation", "publish"},
	} {
		t.Run(tc.args[0], func(t *testing.T) {
			resetJSONOut(t)
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			// macOS ignores XDG_CONFIG_HOME; HOME isolates os.UserConfigDir there.
			t.Setenv("HOME", os.Getenv("XDG_CONFIG_HOME"))
			t.Setenv("FAAS_JSON", "0")
			t.Setenv("FAAS_TOKEN", "test-token")
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != tc.method || r.URL.Path != tc.path {
					t.Errorf("request = %s %s, want %s %s", r.Method, r.URL.Path, tc.method, tc.path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.response)
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			out, stderr, restore := swapIO(t)
			defer restore()
			if code := run(append([]string{"--json"}, tc.args...)); code != 0 {
				t.Fatalf("exit=%d stderr=%s", code, stderr())
			}
			if requests != 1 || stderr() != "" {
				t.Fatalf("requests=%d stderr=%s", requests, stderr())
			}
			var receipt map[string]any
			decoder := json.NewDecoder(out)
			if err := decoder.Decode(&receipt); err != nil {
				t.Fatalf("decode receipt: %v", err)
			}
			got := receipt[tc.field]
			if got != tc.want {
				t.Fatalf("%s=%v, want %v", tc.field, got, tc.want)
			}
			if err := decoder.Decode(&receipt); !errors.Is(err, io.EOF) {
				t.Fatalf("trailing output: %v", err)
			}
		})
	}
}

func TestJSONProjectContextLifecycle(t *testing.T) {
	resetJSONOut(t)
	t.Chdir(t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// macOS ignores XDG_CONFIG_HOME; HOME isolates os.UserConfigDir there.
	t.Setenv("HOME", os.Getenv("XDG_CONFIG_HOME"))
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("FAAS_JSON", "0")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/projects/review" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"slug":"review","workloads":[{"slug":"demo"}]}`)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	out, stderr, restore := swapIO(t)
	defer restore()
	for _, args := range [][]string{{"link", "review", "--app", "demo", "--no-gitignore"}, {"context"}} {
		out.Reset()
		if code := run(append([]string{"--json"}, args...)); code != 0 {
			t.Fatalf("%s exit=%d stderr=%s", args[0], code, stderr())
		}
		var receipt projectContextReceipt
		if err := json.Unmarshal(out.Bytes(), &receipt); err != nil || receipt.Context.Project != "review" || receipt.Context.App != "demo" {
			t.Fatalf("%s receipt=%+v err=%v", args[0], receipt, err)
		}
	}
	out.Reset()
	if code := run([]string{"--json", "unlink"}); code != 0 {
		t.Fatalf("unlink exit=%d stderr=%s", code, stderr())
	}
	var unlinked struct {
		Linked bool   `json:"linked"`
		Path   string `json:"path"`
	}
	if err := json.Unmarshal(out.Bytes(), &unlinked); err != nil || unlinked.Linked || unlinked.Path == "" {
		t.Fatalf("unlink receipt=%+v err=%v", unlinked, err)
	}
	if _, err := os.Stat(filepath.Join(projectContextDirName, projectContextFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("linked context remains: %v", err)
	}
}

func TestJSONSignupAndLogoutReceipts(t *testing.T) {
	resetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	t.Setenv("FAAS_JSON", "0")
	setFakeKeyring(t)
	server, _ := fakeSignupServer(t, http.StatusOK, http.StatusOK)
	t.Setenv("FAAS_API", server.URL)
	pipeStdin(t, "alice@example.com\ncorrect-horse-battery-staple\n")
	out, stderr, restore := swapIO(t)
	defer restore()
	if code := run([]string{"--json", "signup", "--password-stdin"}); code != 0 {
		t.Fatalf("signup exit=%d stderr=%s", code, stderr())
	}
	if !json.Valid(out.Bytes()) || strings.Contains(out.String(), signupPlaintext) {
		t.Fatalf("invalid or secret-bearing receipt: %s", out)
	}
	var account struct {
		Email string `json:"email"`
		ID    string `json:"id"`
	}
	if err := json.Unmarshal(out.Bytes(), &account); err != nil || account.Email != "alice@example.com" || account.ID != "acc_signup_test" {
		t.Fatalf("signup receipt=%+v err=%v", account, err)
	}
	out.Reset()
	if code := run([]string{"--json", "signup", "--email-only", "alice@example.com"}); code != 0 {
		t.Fatalf("magic-link signup exit=%d stderr=%s", code, stderr())
	}
	var magicLink struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(out.Bytes(), &magicLink); err != nil || magicLink.Status != "ok" {
		t.Fatalf("magic-link receipt=%+v err=%v", magicLink, err)
	}
	// Use an environment token so logout clears only this isolated local
	// credential, without needing a server-side revocation endpoint.
	t.Setenv("FAAS_TOKEN", "test-token")
	out.Reset()
	if code := run([]string{"--json", "logout"}); code != 0 {
		t.Fatalf("logout exit=%d stderr=%s", code, stderr())
	}
	var receipt map[string]bool
	if err := json.Unmarshal(out.Bytes(), &receipt); err != nil || !receipt["logged_out"] {
		t.Fatalf("logout receipt=%v err=%v", receipt, err)
	}
}

func TestJSONUploadCacheDryRunReceipt(t *testing.T) {
	resetJSONOut(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// macOS ignores XDG_CONFIG_HOME; HOME isolates os.UserConfigDir there.
	t.Setenv("HOME", os.Getenv("XDG_CONFIG_HOME"))
	t.Setenv("FAAS_JSON", "0")
	out, stderr, restore := swapIO(t)
	defer restore()
	if code := run([]string{"--json", "upload-cache", "cleanup", "--dry-run"}); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr())
	}
	var receipt struct {
		DryRun bool                     `json:"dry_run"`
		Result uploadCacheCleanupResult `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &receipt); err != nil || !receipt.DryRun || receipt.Result.Kept != 0 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}

func TestJSONManUnknownCommand(t *testing.T) {
	resetJSONOut(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// macOS ignores XDG_CONFIG_HOME; HOME isolates os.UserConfigDir there.
	t.Setenv("HOME", os.Getenv("XDG_CONFIG_HOME"))
	t.Setenv("FAAS_JSON", "0")
	out, stderr, restore := swapIO(t)
	defer restore()
	if code := run([]string{"--json", "man", "unknown-command"}); code != 1 {
		t.Fatalf("exit=%d stderr=%s", code, stderr())
	}
	var problem struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal([]byte(stderr()), &problem); err != nil || problem.Code == "" || !strings.Contains(problem.Detail, "unknown command") {
		t.Fatalf("problem=%+v err=%v stderr=%s", problem, err, stderr())
	}
	if out.Len() != 0 {
		t.Fatalf("stdout=%s", out)
	}
}
