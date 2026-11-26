package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// linkedAppServer records request paths and answers every call with an
// empty JSON object, enough for commands that only need to reach the API.
func linkedAppServer(t *testing.T, respond func(w http.ResponseWriter, r *http.Request) bool) *[]string {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if respond != nil && respond(w, r) {
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "test: unhandled"))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	dir := t.TempDir()
	linkCheckoutTo(t, dir, "web")
	t.Chdir(dir)
	return &paths
}

// Prod hunt r53 (rc.381): `cron create --in 1m --path /once` in the app's
// checkout printed `delayed-task add --app` usage; the recurring form used
// the linked app.
func TestOneOffCronUsesTheLinkedApp(t *testing.T) {
	paths := linkedAppServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/apps/web/delayed-tasks" {
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.DelayedTaskResponse{ID: "fe2f184b-d708-4dec-93e8-91457c22aa9e", ScheduledAt: time.Now().Add(time.Minute)})
			return true
		}
		return false
	})
	if code, _, stderr := runCLI(t, "cron", "create", "--in", "1m", "--path", "/once"); code != 0 {
		t.Fatalf("cron create --in = %d:\n%s\nrequests: %q", code, stderr, *paths)
	}
}

// Without a link the one-off form says --app is missing instead of only
// printing another command's usage.
func TestOneOffCronWithoutAppSaysSo(t *testing.T) {
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Chdir(t.TempDir())
	code, _, stderr := runCLI(t, "cron", "create", "--in", "1m", "--path", "/once")
	if code == 0 || !strings.Contains(stderr, "App required") || !strings.Contains(stderr, "--app <slug>") {
		t.Fatalf("cron create --in without app = %d:\n%s", code, stderr)
	}
}

// Prod hunt r53: `env push -f .env` refused a quoted value spanning lines (a
// PEM key) at its second line, though `deploy --secrets-file` accepts it.
func TestEnvPushAcceptsAMultiLineQuotedValue(t *testing.T) {
	var mu sync.Mutex
	bodies := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPut {
			parts := strings.Split(r.URL.Path, "/")
			var body struct {
				Value string `json:"value"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			bodies[parts[len(parts)-1]] = body.Value
			mu.Unlock()
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_ = json.NewEncoder(w).Encode(api.AppSecretListResponse{Quota: 25})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	dir := t.TempDir()
	t.Chdir(dir)
	env := "GREETING=hello\nKEY=\"-----BEGIN KEY-----\nabc\n-----END KEY-----\"\nAFTER=1\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runCLI(t, "env", "push", "--app", "web", "-f", ".env"); code != 0 {
		t.Fatalf("env push = %d:\n%s", code, stderr)
	}
	mu.Lock()
	defer mu.Unlock()
	if bodies["KEY"] != "-----BEGIN KEY-----\nabc\n-----END KEY-----" || bodies["AFTER"] != "1" || bodies["GREETING"] != "hello" {
		t.Fatalf("pushed values = %q", bodies)
	}
}

// Prod hunt r53: `env list` printed "0/100 secrets" above six rows a bucket
// binding manages.
func TestEnvListSaysBindingKeysAreNotCounted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.AppSecretListResponse{Quota: 100, Count: 0, Secrets: []api.AppSecretResponse{
			{Key: "GREGALE_S3_FILES_BUCKET", Scope: "default"}, {Key: "GREGALE_S3_FILES_REGION", Scope: "default"},
		}})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Chdir(t.TempDir())
	code, stdout, stderr := runCLI(t, "env", "list", "--app", "web")
	if code != 0 || !strings.Contains(stdout, "web: 0/100 secrets (keys set by bucket and database bindings do not count toward the quota)") {
		t.Fatalf("env list = %d:\n%s\n%s", code, stdout, stderr)
	}
}

// Prod hunt r53: `release verify <id>`, the check every deploy prints,
// printed usage in the app's checkout without --app.
func TestReleaseVerifyUsesTheLinkedApp(t *testing.T) {
	paths := linkedAppServer(t, nil)
	_, _, stderr := runCLI(t, "release", "verify", "cc1f2bbd-50c2-48ae-a4cd-e14a5f1a24cc")
	if strings.Contains(stderr, "usage:") || len(*paths) == 0 {
		t.Fatalf("release verify did not take the linked app:\n%s\nrequests: %q", stderr, *paths)
	}
}
