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

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Prod hunt r52: a Compose redis service the deploy provides as a managed
// Redis cache was listed under "Managed PostgreSQL (provided by this deploy)".
func TestDryRunListsAProvidedRedisCacheAsRedis(t *testing.T) {
	plan := api.PlanResponse{Managed: []api.PlanManaged{
		{Name: "cache", Kind: "redis", Provision: &api.PlanManagedProvision{Database: "compose-cache",
			Bindings: []api.PlanManagedBinding{{Workload: "api", EnvKey: "REDIS_URL"}}}},
		{Name: "db", Kind: "postgres", Provision: &api.PlanManagedProvision{Database: "compose-db", PostgresMajor: 17,
			Bindings: []api.PlanManagedBinding{{Workload: "api", EnvKey: "DATABASE_URL"}}}},
	}}
	var out strings.Builder
	main.PrintPlanText(&out, plan, nil, false)
	got := out.String()
	pg, redis, _ := strings.Cut(got, "Managed Redis (provided by this deploy):")
	if !strings.Contains(redis, "cache") || !strings.Contains(redis, "creates cache compose-cache, bound to api.REDIS_URL") {
		t.Fatalf("redis cache not listed as managed Redis:\n%s", got)
	}
	if strings.Contains(pg, "compose-cache") || !strings.Contains(pg, "creates compose-db (PostgreSQL 17)") {
		t.Fatalf("PostgreSQL section wrong:\n%s", got)
	}
}

// Prod hunt r52: `postgres suspend-timeout set db 30s` printed only usage.
func TestPostgresSuspendTimeoutSaysWhyAValueIsRefused(t *testing.T) {
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	t.Setenv("FAAS_TOKEN", "test-token")
	for _, value := range []string{"30s", "2h", "90.5s"} {
		code, _, stderr := runCLI(t, "postgres", "suspend-timeout", "set", "orders-db", value)
		if code == 0 || !strings.Contains(stderr, `"`+value+`" is not a whole-second duration from 1m0s to 1h0m0s`) {
			t.Fatalf("suspend-timeout %s = %d:\n%s", value, code, stderr)
		}
	}
}

// Prod hunt r52 (rc.381): `app restart --fresh --wait` on an app whose new
// config crashes it at startup printed running/retrying until its 10 min
// timeout. A startup crash repeats on every retry, so the wait stops at the
// first one and says what to do.
func TestRestartWaitStopsOnStartupCrash(t *testing.T) {
	const wakeID = "5f1c7a52-3d0e-4c4b-9a43-2b7f0f6f1a11"
	statusReads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/web/restart":
			_ = json.NewEncoder(w).Encode(api.AppRestartResponse{WakeID: wakeID})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/web/runtime-config-restarts/"+wakeID:
			statusReads++
			_ = json.NewEncoder(w).Encode(api.RuntimeConfigRestartStatusResponse{
				WakeID: wakeID, Status: "retrying", Attempts: 1, FailureReason: api.CodeAppStartupCrash,
				RequestedAt: time.Now().UTC().Add(-time.Minute),
			})
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":404,"code":"not_found","title":"Not found"}`))
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Chdir(t.TempDir())
	code, _, stderr := runCLI(t, "app", "restart", "web", "--fresh", "--wait", "--poll-interval", "1ms", "--timeout", "30s")
	if code == 0 {
		t.Fatalf("restart wait on a startup crash exited 0:\n%s", stderr)
	}
	if statusReads != 1 {
		t.Fatalf("status read %d times, want the wait to stop at the first startup crash", statusReads)
	}
	for _, want := range []string{"Restart is not taking effect", "crashed during startup", "gregale logs", "gregale env rollback", "gregale env unset"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

// Prod hunt r52: `init --path web --deploy` run from a parent ran `deploy
// --template`, which linked the parent (the cwd) to the new app, so a Compose
// project beside web/ planned itself as that app. A template deploy sends the
// embedded template, not the cwd, and leaves the cwd unlinked; a deploy of
// the cwd itself still remembers the app it created.
func TestTemplateDeployDoesNotLinkTheWorkingDirectory(t *testing.T) {
	var mu sync.Mutex
	created := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/capabilities":
			_ = json.NewEncoder(w).Encode(api.CapabilitiesResponse{Plan: "scale"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps":
			mu.Lock()
			created++
			mu.Unlock()
			var req struct {
				Slug string `json:"slug"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "11111111-1111-4111-8111-111111111111", Slug: req.Slug})
		default:
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "test: unhandled "+r.Method+" "+r.URL.Path))
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("HOME", t.TempDir())

	parent := t.TempDir()
	t.Chdir(parent)
	_, _, _ = runCLI(t, "deploy", "--template", "hello-node", "--name", "tpl-app", "--yes")
	if created == 0 {
		t.Fatal("the template deploy never created its app; the test does not reach the remember step")
	}
	if _, err := os.Stat(filepath.Join(parent, ".gregale", "project.json")); !os.IsNotExist(err) {
		t.Fatalf("template deploy linked the working directory: %v", err)
	}

	// Control: deploying the working directory itself remembers the app.
	own := t.TempDir()
	if err := os.WriteFile(filepath.Join(own, "index.js"), []byte("require('http').createServer((q,s)=>s.end('ok')).listen(process.env.PORT)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(own, "package.json"), []byte(`{"name":"own","scripts":{"start":"node index.js"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(own)
	before := created
	_, _, _ = runCLI(t, "deploy", "--name", "own-app", "--yes")
	if created == before {
		t.Skip("the source deploy did not reach app creation against the fake server")
	}
	if _, err := os.Stat(filepath.Join(own, ".gregale", "project.json")); err != nil {
		t.Fatalf("a deploy of the working directory did not remember its app: %v", err)
	}
}
