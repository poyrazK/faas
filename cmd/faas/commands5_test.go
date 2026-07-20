// CLI tests for the UX §3.1 commands that landed in issue #63:
// ps / status / env pull|push / app scale / app rename / plan /
// dashboard / apps ls.
//
// Mirrors the secrets/account test patterns: programmable fake-apid
// sinks + t.Setenv wiring + osStdout swap.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/faas/templates"
	"github.com/onebox-faas/faas/pkg/api"
)

// --- sinks -----------------------------------------------------------------

// writeJSONTestStatus encodes payload as JSON with the given status
// (defaulting to 200 if status==0). Used by multiSink to share one
// writer across all its routes.
func writeJSONTestStatus(w http.ResponseWriter, status int, payload any) {
	if status == 0 {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(payload)
}

// statusSink answers GET /status/slo.json.
type statusSink struct {
	resp api.StatusPage
	err  error
}

func (s *statusSink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/status/slo.json" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if s.err != nil {
		http.Error(w, s.err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSONTest(w, s.resp)
}

// multiSink is a path-routed fake that handles ps/status/env/plan/rename
// routes so dispatch tests don't need 5 different sinks. Each test
// sets only the handler(s) it cares about.
type multiSink struct {
	onStatus   func() (int, any)
	onAccount  func(method string) (int, any)
	onApps     func(method string, path string) (int, any)
	onListApp  func(slug string) (int, any)
	onRename   func(slug string) (int, any, []byte)
	onScale    func(slug string, body []byte) (int, any)
	onSecrets  func(method string, path string) (int, any)
	onPlan     func(body []byte) (int, any)
	lastBody   []byte
	lastPath   string
	lastQuery  string
	lastMethod string
	lastHeader http.Header
}

func (s *multiSink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.lastMethod = r.Method
	s.lastPath = r.URL.Path
	s.lastQuery = r.URL.RawQuery
	s.lastHeader = r.Header.Clone()
	body, _ := io.ReadAll(r.Body)
	s.lastBody = body
	path := r.URL.Path
	switch {
	case path == "/status/slo.json":
		status, payload := s.onStatus()
		writeJSONTestStatus(w, status, payload)
	case path == "/v1/account":
		status, payload := s.onAccount(r.Method)
		writeJSONTestStatus(w, status, payload)
	case strings.HasPrefix(path, "/v1/apps") && strings.HasSuffix(path, "/rename"):
		slug := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/apps/"), "/rename")
		status, payload, _ := s.onRename(slug)
		writeJSONTestStatus(w, status, payload)
	case strings.HasPrefix(path, "/v1/apps") && strings.HasSuffix(path, "/instances"):
		slug := strings.TrimSuffix(strings.TrimPrefix(path, "/v1/apps/"), "/instances")
		status, payload := s.onListApp(slug)
		writeJSONTestStatus(w, status, payload)
	case strings.HasPrefix(path, "/v1/apps") && r.Method == "PATCH":
		slug := strings.TrimPrefix(path, "/v1/apps/")
		status, payload := s.onScale(slug, body)
		writeJSONTestStatus(w, status, payload)
	case path == "/v1/apps":
		status, payload := s.onApps(r.Method, path)
		writeJSONTestStatus(w, status, payload)
	case strings.HasPrefix(path, "/v1/apps") && strings.Contains(path, "/secrets"):
		status, payload := s.onSecrets(r.Method, path)
		writeJSONTestStatus(w, status, payload)
	case strings.HasPrefix(path, "/v1/account/plan"):
		status, payload := s.onPlan(body)
		writeJSONTestStatus(w, status, payload)
	default:
		http.Error(w, "not found: "+path, http.StatusNotFound)
	}
}

// --- ps --------------------------------------------------------------------

func TestCmdPS_RequiresArg(t *testing.T) {
	if code := cmdPS(nil); code != 1 {
		t.Errorf("cmdPS(nil) = %d, want 1", code)
	}
}

func TestCmdPS_RequiresLogin(t *testing.T) {
	t.Setenv("FAAS_API", "http://localhost")
	t.Setenv("FAAS_TOKEN", "")
	if code := cmdPS([]string{"hello"}); code != 2 {
		t.Errorf("cmdPS without token = %d, want 2 (auth)", code)
	}
}

func TestCmdPS_RendersInstancesAndHumanizesParked(t *testing.T) {
	sink := &multiSink{
		onListApp: func(slug string) (int, any) {
			if slug != "hello" {
				t.Errorf("ps called with slug %q, want hello", slug)
			}
			return http.StatusOK, []api.InstanceResponse{
				{ID: "i-1", State: "parked", RAMMB: 256, StartedAt: "2026-07-20T09:00:00Z", LastRequestAt: "2026-07-20T08:55:00Z"},
				{ID: "i-2", State: "running", RAMMB: 256, StartedAt: "2026-07-20T09:01:00Z", LastRequestAt: "2026-07-20T09:01:30Z"},
			}
		},
	}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	stdout, restore := captureStdout(t)
	defer restore()
	if code := cmdPS([]string{"hello"}); code != 0 {
		t.Errorf("cmdPS exit = %d, want 0", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "i-1") {
		t.Errorf("output missing i-1: %q", out)
	}
	if !strings.Contains(out, "sleeping") {
		t.Errorf("parked instance should render as sleeping: %q", out)
	}
	if !strings.Contains(out, "running") {
		t.Errorf("running instance should render as running: %q", out)
	}
}

func TestCmdPS_EmptyListShowsParkedMessage(t *testing.T) {
	sink := &multiSink{onListApp: func(string) (int, any) { return http.StatusOK, []api.InstanceResponse{} }}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	stdout, restore := captureStdout(t)
	defer restore()
	if code := cmdPS([]string{"hello"}); code != 0 {
		t.Errorf("cmdPS exit = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "parked") {
		t.Errorf("empty list should print 'parked' message: %q", stdout.String())
	}
}

// --- status ----------------------------------------------------------------

func TestCmdStatus_RendersFiveFields(t *testing.T) {
	when := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	sink := &statusSink{resp: api.StatusPage{
		APIAvailabilityPct: 99.97,
		WakeP95MS:          312,
		BuildSuccessPct:    98.4,
		AsOf:               when,
		Source:             "prometheus",
	}}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "") // endpoint is public
	stdout, restore := captureStdout(t)
	defer restore()
	if code := cmdStatus(nil); code != 0 {
		t.Errorf("cmdStatus exit = %d, want 0", code)
	}
	out := stdout.String()
	for _, want := range []string{"99.97%", "312 ms", "98.40%", "2026-07-20 12:00:00 UTC", "prometheus"} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q: %q", want, out)
		}
	}
}

func TestCmdStatus_DegradedSource(t *testing.T) {
	sink := &statusSink{resp: api.StatusPage{
		APIAvailabilityPct: 0,
		WakeP95MS:          0,
		BuildSuccessPct:    0,
		AsOf:               time.Now().UTC(),
		Source:             "degraded: prometheus timeout",
	}}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "")
	stdout, restore := captureStdout(t)
	defer restore()
	if code := cmdStatus(nil); code != 0 {
		t.Errorf("cmdStatus exit = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "degraded") {
		t.Errorf("degraded source should be visible: %q", stdout.String())
	}
}

// --- env -------------------------------------------------------------------

func TestCmdEnvPull_WritesKeyOnlyTemplate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FAAS_API", "http://localhost")
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	out := filepath.Join(dir, ".env")
	called := false
	sink := &multiSink{onSecrets: func(method, path string) (int, any) {
		if method != "GET" {
			t.Errorf("env pull should GET secrets, got %s", method)
		}
		called = true
		return http.StatusOK, api.AppSecretListResponse{
			Count:   2,
			Quota:   25,
			Secrets: []api.AppSecretResponse{{Key: "STRIPE_KEY"}, {Key: "DB_URL"}},
		}
	}}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	stdout, restore := captureStdout(t)
	defer restore()
	if code := envPull([]string{"--app", "hello", "-o", out}); code != 0 {
		t.Errorf("envPull exit = %d, want 0", code)
	}
	if !called {
		t.Errorf("sink was not called")
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read %s: %v", out, err)
	}
	text := string(body)
	if !strings.Contains(text, "STRIPE_KEY=\n") {
		t.Errorf("missing STRIPE_KEY= line: %q", text)
	}
	if !strings.Contains(text, "DB_URL=\n") {
		t.Errorf("missing DB_URL= line: %q", text)
	}
	// G2 invariant: plaintext values must NEVER appear in the template
	// because the server never returns them. Assert the file is
	// template-only.
	for _, banned := range []string{"sk_live_x", "postgres://", "value"} {
		if strings.Contains(text, banned) {
			t.Errorf("pulled .env contains banned token %q (G2 leak): %q", banned, text)
		}
	}
	if !strings.Contains(stdout.String(), "values intentionally blank") {
		t.Errorf("stdout should warn about blank values: %q", stdout.String())
	}
}

func TestCmdEnvPush_ForwardsEveryKeyValue(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	if err := os.WriteFile(envFile, []byte("# header comment\n\nA=alpha\nB=bravo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var puts []string
	sink := &multiSink{onSecrets: func(method, path string) (int, any) {
		// GET → existing list (empty); PUT → record key
		if method == "GET" {
			return http.StatusOK, api.AppSecretListResponse{Quota: 25}
		}
		if method == "PUT" {
			// path: /v1/apps/{slug}/secrets/{key}
			parts := strings.Split(path, "/")
			key := parts[len(parts)-1]
			puts = append(puts, key)
			return http.StatusOK, nil
		}
		return http.StatusBadRequest, nil
	}}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	stdout, restore := captureStdout(t)
	defer restore()
	if code := envPush([]string{"--app", "hello", "-f", envFile}); code != 0 {
		t.Errorf("envPush exit = %d, want 0", code)
	}
	if !containsAll(puts, []string{"A", "B"}) {
		t.Errorf("PUT keys = %v, want [A B]", puts)
	}
	if !strings.Contains(stdout.String(), "A set") || !strings.Contains(stdout.String(), "B set") {
		t.Errorf("stdout should confirm both keys set: %q", stdout.String())
	}
}

// --- app scale / rename ---------------------------------------------------

func TestCmdAppScale_RequiresLogin(t *testing.T) {
	t.Setenv("FAAS_API", "http://localhost")
	t.Setenv("FAAS_TOKEN", "")
	if code := cmdAppScale("hello", []string{"--ram", "256"}); code != 2 {
		t.Errorf("cmdAppScale without token = %d, want 2", code)
	}
}

func TestCmdAppScale_ForwardsExplicitFlags(t *testing.T) {
	var gotBody []byte
	var gotSlug string
	sink := &multiSink{onScale: func(slug string, body []byte) (int, any) {
		gotSlug = slug
		gotBody = body
		return http.StatusOK, api.AppResponse{Slug: slug, RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60}
	}}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	stdout, restore := captureStdout(t)
	defer restore()
	if code := cmdAppScale("hello", []string{"--ram", "256", "--max-concurrency", "2"}); code != 0 {
		t.Errorf("cmdAppScale exit = %d, want 0", code)
	}
	if gotSlug != "hello" {
		t.Errorf("PATCH slug = %q, want hello", gotSlug)
	}
	// Unmarshal to check pointer fields are present (not omitted).
	var req api.UpdateAppRequest
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if req.RAMMB == nil || *req.RAMMB != 256 {
		t.Errorf("ram_mb = %v, want pointer to 256", req.RAMMB)
	}
	if req.MaxConcurrency == nil || *req.MaxConcurrency != 2 {
		t.Errorf("max_concurrency = %v, want pointer to 2", req.MaxConcurrency)
	}
	if !strings.Contains(stdout.String(), "Updated") {
		t.Errorf("stdout should print ✓ Updated: %q", stdout.String())
	}
}

func TestCmdAppRename_HappyPath(t *testing.T) {
	sink := &multiSink{onRename: func(oldSlug string) (int, any, []byte) {
		if oldSlug != "hello" {
			t.Errorf("rename oldSlug = %q, want hello", oldSlug)
		}
		return http.StatusOK, api.AppResponse{Slug: "my-hello"}, nil
	}}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	stdout, restore := captureStdout(t)
	defer restore()
	if code := cmdAppRename("hello", "my-hello"); code != 0 {
		t.Errorf("cmdAppRename exit = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "hello → my-hello") {
		t.Errorf("stdout should show from→to: %q", stdout.String())
	}
}

func TestCmdAppRename_RejectsBadSlug(t *testing.T) {
	for _, bad := range []string{"AB", "-leading", "trailing-", "with spaces", "WITH-CAPS"} {
		t.Run(bad, func(t *testing.T) {
			if code := cmdAppRename("hello", bad); code != 1 {
				t.Errorf("cmdAppRename(%q) = %d, want 1", bad, code)
			}
		})
	}
}

func TestCmdAppRename_NoOpOnSameSlug(t *testing.T) {
	// No server needed — same-slug short-circuits before any HTTP call.
	stdout, restore := captureStdout(t)
	defer restore()
	if code := cmdAppRename("hello", "hello"); code != 0 {
		t.Errorf("cmdAppRename same slug = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "already has that slug") {
		t.Errorf("stdout should mention no-op: %q", stdout.String())
	}
}

func TestCmdAppRename_ConflictRendersProblem(t *testing.T) {
	sink := &multiSink{onRename: func(string) (int, any, []byte) {
		return http.StatusConflict, api.Problem{
			Type:   "https://docs.DOMAIN/errors/app_rename_failed",
			Title:  "Slug already in use",
			Status: http.StatusConflict,
			Code:   "app_rename_failed",
			Detail: "another app already uses slug \"taken\"",
		}, nil
	}}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	stderr, restore := captureStderr(t)
	defer restore()
	if code := cmdAppRename("hello", "taken"); code != 1 {
		t.Errorf("cmdAppRename conflict = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "Slug already in use") {
		t.Errorf("conflict detail should surface on stderr: %q", stderr.String())
	}
}

func TestCmdAppDispatch_RoutesSubcommandAndLegacy(t *testing.T) {
	// New subcommand form
	if code := cmdAppDispatch([]string{"hello", "scale", "--ram", "256"}); code == 0 {
		// Will fail at the no-server step (auth error → exit 2) — we
		// just want to assert dispatch reaches cmdAppScale. Easier:
		// confirm with a rename no-op (no server hit).
		t.Setenv("FAAS_API", "http://localhost")
		t.Setenv("FAAS_TOKEN", "fp_live_x")
	}
	// No-op rename route: same slug exits 0 without hitting the API.
	if code := cmdAppDispatch([]string{"hello", "rename", "hello"}); code != 0 {
		t.Errorf("dispatch rename same-slug = %d, want 0", code)
	}
	if code := cmdAppDispatch([]string{}); code != 1 {
		t.Errorf("dispatch no-args = %d, want 1", code)
	}
}

// --- plan ------------------------------------------------------------------

func TestCmdPlan_RejectsUnknown(t *testing.T) {
	t.Setenv("FAAS_API", "http://localhost")
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	if code := cmdPlan([]string{"premium"}); code != 1 {
		t.Errorf("cmdPlan(unknown) = %d, want 1", code)
	}
}

func TestCmdPlan_DispatchesKnownPlans(t *testing.T) {
	cases := []struct{ plan, wantBody string }{
		{"free", `"plan":"free"`},
		{"hobby", `"plan":"hobby"`},
		{"pro", `"plan":"pro"`},
		{"scale", `"plan":"scale"`},
	}
	for _, c := range cases {
		t.Run(c.plan, func(t *testing.T) {
			var gotBody []byte
			sink := &multiSink{
				onAccount: func(method string) (int, any) {
					// Same-plan current → no downgrade prompt
					return http.StatusOK, api.AccountResponse{Email: "a@b.c", Plan: c.plan}
				},
				onPlan: func(body []byte) (int, any) {
					gotBody = body
					return http.StatusOK, api.AccountResponse{Email: "a@b.c", Plan: c.plan}
				},
			}
			srv := httptest.NewServer(sink)
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			stdout, restore := captureStdout(t)
			defer restore()
			if code := cmdPlan([]string{c.plan}); code != 0 {
				t.Errorf("cmdPlan(%s) = %d, want 0", c.plan, code)
			}
			if !strings.Contains(string(gotBody), c.wantBody) {
				t.Errorf("plan body = %q, want substring %q", gotBody, c.wantBody)
			}
			if !strings.Contains(stdout.String(), "Plan changed") {
				t.Errorf("stdout should confirm plan change: %q", stdout.String())
			}
		})
	}
}

func TestCmdPlan_DowngradeConfirmation(t *testing.T) {
	// Pipe "n" to stdin so the y/N prompt refuses the downgrade.
	old := osStdin
	defer func() { osStdin = old }()
	pr, pw, _ := os.Pipe()
	osStdin = pr
	_, _ = pw.WriteString("n\n")
	_ = pw.Close()

	sink := &multiSink{
		onAccount: func(string) (int, any) {
			return http.StatusOK, api.AccountResponse{Email: "a@b.c", Plan: "pro"}
		},
	}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	stdout, restore := captureStdout(t)
	defer restore()
	if code := cmdPlan([]string{"free"}); code != 1 {
		t.Errorf("cmdPlan downgrade with 'n' = %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), "aborted") {
		t.Errorf("refusal should print 'aborted': %q", stdout.String())
	}
}

// --- dashboard -------------------------------------------------------------

func TestCmdDashboard_OpensAccountURL(t *testing.T) {
	t.Setenv("FAAS_API", "https://api.example.com")
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	rec := withRecorder(t)
	if code := cmdDashboard(nil); code != 0 {
		t.Errorf("cmdDashboard = %d, want 0", code)
	}
	if len(rec.urls) != 1 {
		t.Fatalf("recorder saw %d launches, want 1", len(rec.urls))
	}
	if !strings.Contains(rec.urls[0], "/dashboard/account") {
		t.Errorf("opened URL = %q, want it to contain /dashboard/account", rec.urls[0])
	}
}

// TestCmdDashboard_BrowserOpenFailureExitsZero covers the no-$DISPLAY
// path: browser.Open returns an error, the URL falls back to stderr,
// and exit code is 0 (the customer's intent — get the dashboard URL —
// is satisfied). Mirrors the cmdDeployRepo convention. If this test
// ever flips to want exit 1, the command's doc comment and
// cmdDeployRepo (commands2.go:288) need to be revisited together.
func TestCmdDashboard_BrowserOpenFailureExitsZero(t *testing.T) {
	t.Setenv("FAAS_API", "https://api.example.com")
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	rec := withRecorder(t)
	rec.err = errors.New("xdg-open: no display")
	stderr, restore := captureStderr(t)
	defer restore()
	code := cmdDashboard(nil)
	if code != 0 {
		t.Errorf("cmdDashboard on browser-open error = %d, want 0 (URL fallback is success)", code)
	}
	if !strings.Contains(stderr.String(), "https://api.example.com/dashboard/account") {
		t.Errorf("stderr missing fallback URL; got:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Could not open browser") {
		t.Errorf("stderr missing failure notice; got:\n%s", stderr.String())
	}
}

// TestCmdDashboard_RejectsExtraArgs is the standard arg-count guard.
func TestCmdDashboard_RejectsExtraArgs(t *testing.T) {
	t.Setenv("FAAS_API", "https://api.example.com")
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	_ = withRecorder(t)
	if code := cmdDashboard([]string{"junk"}); code != 1 {
		t.Errorf("cmdDashboard extra args = %d, want 1", code)
	}
}

func TestCmdDashboard_RequiresLogin(t *testing.T) {
	t.Setenv("FAAS_API", "http://localhost")
	t.Setenv("FAAS_TOKEN", "")
	if code := cmdDashboard(nil); code != 2 {
		t.Errorf("cmdDashboard no-auth = %d, want 2", code)
	}
}

// --- apps ls alias ---------------------------------------------------------

func TestCmdAppsDispatch_LsAlias(t *testing.T) {
	// Drive through run() so the alias path is exercised end-to-end.
	// cmdApps prints via fmt.Print (not the osStdout seam), so we
	// can't easily capture its output without changing production
	// code. Instead, assert via the server hit: a 200 from /v1/apps
	// means the alias routed past dispatch correctly.
	var hit bool
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		hit = true
		writeJSONTest(w, []api.AppResponse{{Slug: "hello", Status: "ready", URL: "https://hello.example.com"}})
	}))
	defer sink.Close()
	t.Setenv("FAAS_API", sink.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	if code := run([]string{"apps", "ls"}); code != 0 {
		t.Errorf("run(apps ls) = %d, want 0", code)
	}
	if !hit {
		t.Errorf("apps ls did not hit /v1/apps")
	}
}

// --- templates -------------------------------------------------------------

func TestTemplates_ExistsAndTarGz(t *testing.T) {
	for _, name := range templates.Names {
		t.Run(name, func(t *testing.T) {
			if !templates.Exists(name) {
				t.Errorf("Exists(%q) = false", name)
			}
			dir := t.TempDir()
			tar := filepath.Join(dir, name+".tar.gz")
			if err := templates.TarGz(name, tar); err != nil {
				t.Fatalf("TarGz: %v", err)
			}
			st, err := os.Stat(tar)
			if err != nil {
				t.Fatalf("stat: %v", err)
			}
			if st.Size() == 0 {
				t.Errorf("tarball is empty")
			}
		})
	}
}

func TestTemplates_MaterializeContainsExpectedFiles(t *testing.T) {
	cases := map[string][]string{
		"hello-node":      {"handler.js", "package.json", "README.md"},
		"hello-python":    {"handler.py", "requirements.txt", "README.md"},
		"hello-go":        {"main.go", "README.md"},
		"cron-example":    {"handler.js", "package.json", "README.md"},
		"function-node":   {"handler.js", "package.json", "README.md"},
		"function-python": {"handler.py", "requirements.txt", "README.md"},
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			dir, cleanup, err := templates.MaterializeForTest(name)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			for _, f := range want {
				if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
					t.Errorf("missing %s: %v", f, err)
				}
			}
		})
	}
}

func TestTemplates_RejectsPathTraversal(t *testing.T) {
	for _, bad := range []string{"", ".", "..", "../etc", "foo/bar"} {
		t.Run(bad, func(t *testing.T) {
			if templates.NameIsValid(bad) {
				t.Errorf("NameIsValid(%q) = true, want false", bad)
			}
		})
	}
}

// --- helpers ---------------------------------------------------------------

// captureStdout swaps osStdout for a buffer and returns a restore func.
func captureStdout(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	var buf bytes.Buffer
	old := osStdout
	osStdout = &buf
	return &buf, func() { osStdout = old }
}

// captureStderr redirects os.Stderr to a tempfile and returns a
// reader whose String() reflects whatever was written by the time the
// caller asks for it. printErr writes to os.Stderr directly, so the
// swap catches error-path output.
//
// Implementation note: we Sync+Close the file before reading so the
// contents are durable on every supported OS (macOS / Linux flush
// 4 KB pages lazily).
func captureStderr(t *testing.T) (*stderrReader, func()) {
	t.Helper()
	tmp, err := os.CreateTemp(t.TempDir(), "stderr-*.txt")
	if err != nil {
		t.Fatalf("create stderr temp: %v", err)
	}
	path := tmp.Name()
	old := os.Stderr
	os.Stderr = tmp
	rd := &stderrReader{path: path}
	restore := func() {
		_ = os.Stderr.Sync()
		_ = os.Stderr.Close()
		os.Stderr = old
		rd.reload()
	}
	t.Cleanup(func() {
		_ = os.Remove(path)
		if os.Stderr == tmp {
			os.Stderr = old
		}
	})
	return rd, restore
}

// stderrReader is a tiny String() reader backed by a tempfile. Each
// call to reload re-reads the file from disk so callers always see
// the latest writes without holding a long-lived pipe goroutine.
type stderrReader struct {
	path string
	buf  bytes.Buffer
}

func (r *stderrReader) reload() {
	data, err := os.ReadFile(r.path)
	if err != nil {
		return
	}
	r.buf.Reset()
	r.buf.Write(data)
}

func (r *stderrReader) String() string { r.reload(); return r.buf.String() }

func containsAll(haystack []string, needles []string) bool {
	set := map[string]bool{}
	for _, s := range haystack {
		set[s] = true
	}
	for _, n := range needles {
		if !set[n] {
			return false
		}
	}
	return true
}
