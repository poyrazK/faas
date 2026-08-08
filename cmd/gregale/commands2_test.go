package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/browser"
	"github.com/onebox-faas/faas/pkg/wire"
)

// constSlug lifts "hello" out of the test bodies so goconst stops flagging
// the repeated literal across request bodies, AppResponse fixtures, and the
// GET-path assertion.
const constSlug = "hello"

// TestCmdAppFlagSentinels exercises cmdApp's flag parsing. The CLI must:
//   - send an explicit `--ram 0` as a non-nil pointer (the wire form distinguishes
//     unset from zero via *int);
//   - take the GET path when no flags are passed;
//   - only send PATCH when at least one flag was provided.
//
// We don't reach apid/auth in this test — we redirect the API base to a local
// httptest server via FAAS_API and inject a fake token via FAAS_TOKEN, then
// capture the request body the client would have sent.
func TestCmdAppFlagSentinels(t *testing.T) {
	type captured struct {
		method string
		path   string
		body   api.UpdateAppRequest
	}
	var (
		mu  sync.Mutex
		got captured
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// GET /v1/apps/{slug} — show path
		if r.Method == http.MethodGet {
			writeJSONTest(w, api.AppResponse{Slug: constSlug})
			return
		}
		// PATCH /v1/apps/{slug} — update path
		body, _ := io.ReadAll(r.Body)
		var req api.UpdateAppRequest
		_ = json.Unmarshal(body, &req)
		mu.Lock()
		got = captured{method: r.Method, path: r.URL.Path, body: req}
		mu.Unlock()
		writeJSONTest(w, api.AppResponse{Slug: constSlug})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")

	cases := []struct {
		name        string
		args        []string
		wantMethod  string
		wantRAMSet  bool
		wantRAMVal  int
		wantIdleSet bool
		wantIdleVal int
		wantMinSet  bool
		wantMinVal  int
	}{
		{
			name:       "no flags → GET path",
			args:       []string{constSlug},
			wantMethod: http.MethodGet,
		},
		{
			name:       "--ram 0 is explicit zero (must NOT be dropped)",
			args:       []string{constSlug, "--ram", "0"},
			wantMethod: http.MethodPatch,
			wantRAMSet: true,
			wantRAMVal: 0,
		},
		{
			name:       "--ram 256 is positive",
			args:       []string{constSlug, "--ram", "256"},
			wantMethod: http.MethodPatch,
			wantRAMSet: true,
			wantRAMVal: 256,
		},
		{
			name:        "--idle -1 is explicit negative (must NOT be dropped)",
			args:        []string{constSlug, "--idle", "-1"},
			wantMethod:  http.MethodPatch,
			wantIdleSet: true,
			wantIdleVal: -1,
		},
		{
			name:       "--min 0 is explicit zero (scale to zero; must NOT be dropped)",
			args:       []string{constSlug, "--min", "0"},
			wantMethod: http.MethodPatch,
			wantMinSet: true,
			wantMinVal: 0,
		},
		{
			name:       "--min 1 is positive",
			args:       []string{constSlug, "--min", "1"},
			wantMethod: http.MethodPatch,
			wantMinSet: true,
			wantMinVal: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mu.Lock()
			got = captured{}
			mu.Unlock()

			if code := cmdApp(tc.args); code != 0 {
				t.Fatalf("cmdApp exit = %d, want 0", code)
			}

			mu.Lock()
			defer mu.Unlock()
			if got.method == "" && tc.wantMethod == http.MethodGet {
				return // GET path doesn't populate got
			}
			if got.method != tc.wantMethod {
				t.Fatalf("method = %q, want %q", got.method, tc.wantMethod)
			}
			if tc.wantRAMSet {
				if got.body.RAMMB == nil {
					t.Fatalf("RAMMB = nil; expected pointer to %d", tc.wantRAMVal)
				}
				if *got.body.RAMMB != tc.wantRAMVal {
					t.Errorf("RAMMB = %d, want %d", *got.body.RAMMB, tc.wantRAMVal)
				}
			} else if got.body.RAMMB != nil {
				t.Errorf("RAMMB = %d, want nil", *got.body.RAMMB)
			}
			if tc.wantIdleSet {
				if got.body.IdleTimeoutS == nil {
					t.Fatalf("IdleTimeoutS = nil; expected pointer to %d", tc.wantIdleVal)
				}
				if *got.body.IdleTimeoutS != tc.wantIdleVal {
					t.Errorf("IdleTimeoutS = %d, want %d", *got.body.IdleTimeoutS, tc.wantIdleVal)
				}
			} else if got.body.IdleTimeoutS != nil {
				t.Errorf("IdleTimeoutS = %d, want nil", *got.body.IdleTimeoutS)
			}
			if tc.wantMinSet {
				if got.body.MinInstances == nil {
					t.Fatalf("MinInstances = nil; expected pointer to %d", tc.wantMinVal)
				}
				if *got.body.MinInstances != tc.wantMinVal {
					t.Errorf("MinInstances = %d, want %d", *got.body.MinInstances, tc.wantMinVal)
				}
			} else if got.body.MinInstances != nil {
				t.Errorf("MinInstances = %d, want nil", *got.body.MinInstances)
			}
		})
	}
}

// TestCmdAppMinInstances_HobbyRejects is the wire-level CLI check for
// the plan-tier gate (ux_spec §6.5). When apid returns 403
// plan_min_instances_not_allowed, the CLI must surface a non-zero exit
// code so scripts/cron-on-CI can detect the failure without parsing
// prose. The CLI is a thin wrapper over apid — the gate is the gate —
// but the exit-code mapping is CLI-only behaviour worth pinning.
func TestCmdAppMinInstances_HobbyRejects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"type":"about:blank","title":"plan","status":403,"code":"plan_min_instances_not_allowed"}`)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")

	one := 1
	if code := cmdApp([]string{constSlug, "--min", itoaForCli(one)}); code == 0 {
		t.Fatalf("cmdApp exit = 0; want non-zero (api rejected 403)")
	}
}

// TestCmdAppPublicAuth_ParsesAndForwards wires the --public-auth
// flag (issue #477 / ADR-079). Three sub-cases pin the
// customer-facing surface:
//
//  1. Unknown mode -> CLI-side typo rejection (no round-trip).
//  2. mode='basic' without --basic-user -> CLI-side missing-creds
//     rejection (no round-trip).
//  3. mode='basic' with creds -> wire shape carries the
//     PublicAuthBlock exactly as the apid handler expects
//     (mode + basic_user + basic_pass as plaintext — the apid
//     seal step handles APP_BASIC_AUTH encryption server-side).
//
// The third case asserts the wire shape so a future contributor
// adding a flag-by-flag emit doesn't accidentally drop basic_user
// or basic_pass from the JSON body.
func TestCmdAppPublicAuth_ParsesAndForwards(t *testing.T) {
	t.Run("unknown_mode_rejected_locally", func(t *testing.T) {
		called := false
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()
		t.Setenv("FAAS_API", srv.URL)
		t.Setenv("FAAS_TOKEN", "fp_test_x")
		if code := cmdApp([]string{constSlug, "--public-auth", "weird"}); code == 0 {
			t.Fatalf("cmdApp --public-auth=weird should reject locally; round-trip happened = %v", called)
		}
	})
	t.Run("basic_mode_requires_user", func(t *testing.T) {
		called := false
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()
		t.Setenv("FAAS_API", srv.URL)
		t.Setenv("FAAS_TOKEN", "fp_test_x")
		if code := cmdApp([]string{constSlug, "--public-auth", "basic", "--basic-pass", "x"}); code == 0 {
			t.Fatalf("cmdApp --public-auth=basic without --basic-user should reject; round-trip = %v", called)
		}
	})
	t.Run("basic_mode_forwards_block", func(t *testing.T) {
		var seen *api.UpdateAppRequest
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPatch {
				var body api.UpdateAppRequest
				_ = json.NewDecoder(r.Body).Decode(&body)
				seen = &body
			}
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"slug":"hello","public_auth":{"mode":"basic","has_basic_creds":true}}`)
		}))
		defer srv.Close()
		t.Setenv("FAAS_API", srv.URL)
		t.Setenv("FAAS_TOKEN", "fp_test_x")
		if code := cmdApp([]string{constSlug,
			"--public-auth", "basic",
			"--basic-user", "editor",
			"--basic-pass", "hunter2",
		}); code != 0 {
			t.Fatalf("cmdApp --public-auth=basic + creds exit = %d; want 0", code)
		}
		if seen == nil || seen.PublicAuth == nil {
			t.Fatalf("cmdApp did not send PublicAuth block; got %+v", seen)
		}
		if seen.PublicAuth.Mode != api.AppPublicAuthModeBasic {
			t.Fatalf("PublicAuth.Mode = %q; want %q", seen.PublicAuth.Mode, api.AppPublicAuthModeBasic)
		}
		if seen.PublicAuth.BasicUser != "editor" {
			t.Fatalf("PublicAuth.BasicUser = %q; want %q", seen.PublicAuth.BasicUser, "editor")
		}
		if seen.PublicAuth.BasicPass != "hunter2" {
			t.Fatalf("PublicAuth.BasicPass = %q; want %q", seen.PublicAuth.BasicPass, "hunter2")
		}
	})
}

// TestCmdTrafficSet_BasicFlow (issue #556 PR-A) is the wire-level
// CLI check for `gregale traffic set --deployment <id> --percent N`.
// Pins:
//  1. CLI dispatches to cmdTrafficSet.
//  2. PATCH /v1/deployments/{id}/traffic is called with the
//     canonical body shape ({"traffic_percent": N}).
//  3. The 200 response renders as the canonical "Set … → N%" line.
func TestCmdTrafficSet_BasicFlow(t *testing.T) {
	const wantDepID = "0123456789abcdef0123456789abcdef"
	const wantPercent = 25
	var hits int32
	var gotMethod, gotPath, gotBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotMethod = r.Method
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		writeJSONTest(w, api.DeploymentResponse{
			ID:             wantDepID,
			AppID:          "app-id",
			TrafficPercent: wantPercent,
		})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")

	if code := cmdTrafficSet([]string{"--deployment", wantDepID, "--percent", itoaForCli(wantPercent)}); code != 0 {
		t.Fatalf("cmdTrafficSet exit = %d, want 0", code)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("PATCH hit count = %d, want 1", hits)
	}
	if gotMethod != "PATCH" {
		t.Errorf("method = %q, want PATCH", gotMethod)
	}
	if gotPath != "/v1/deployments/"+wantDepID+"/traffic" {
		t.Errorf("path = %q, want /v1/deployments/%s/traffic", gotPath, wantDepID)
	}
	wantBody := `{"traffic_percent":25}`
	if gotBody != wantBody {
		t.Errorf("body = %q, want %q", gotBody, wantBody)
	}
}

// TestCmdTrafficSet_MissingArgs (issue #556 PR-A) pins the CLI's
// flag-presence contract. The subcommand must reject missing
// --deployment or --percent before any HTTP round-trip — the
// existing TestCmdAppFlagSentinels / TestCmdAppPublicAuth patterns
// treat this as a CLI-side correctness check.
func TestCmdTrafficSet_MissingArgs(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")

	// Missing --percent.
	if code := cmdTrafficSet([]string{"--deployment", "x"}); code == 0 {
		t.Errorf("missing --percent exit = 0, want non-zero")
	}
	// Missing --deployment.
	if code := cmdTrafficSet([]string{"--percent", "50"}); code == 0 {
		t.Errorf("missing --deployment exit = 0, want non-zero")
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Errorf("server was hit %d times; CLI must short-circuit before HTTP", hits)
	}
}

// itoaForCli is a tiny local helper for the Hobby-rejects test so the
// file doesn't depend on strconv (matches the apid test's itoa style).
func itoaForCli(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	pos := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// --- slice 9: validateRepoSlug, cmdConnect, cmdOpen, --repo dispatch -------

// recordedLauncher is a stub browser.Launcher that records URLs
// instead of exec'ing xdg-open/open/start.
type recordedLauncher struct {
	urls []string
	err  error
}

func (r *recordedLauncher) Launch(url string) error {
	r.urls = append(r.urls, url)
	return r.err
}

func withRecorder(t *testing.T) *recordedLauncher {
	t.Helper()
	rec := &recordedLauncher{}
	old := browser.Default
	browser.Default = rec
	t.Cleanup(func() { browser.Default = old })
	return rec
}

func TestValidateRepoSlug_AcceptsCanonical(t *testing.T) {
	cases := []string{
		"octo/api",
		"jane.doe/my_app",
		"my-org/some.repo.name",
	}
	for _, s := range cases {
		if err := validateRepoSlug(s); err != nil {
			t.Errorf("validateRepoSlug(%q) = %v, want nil", s, err)
		}
	}
}

func TestValidateRepoSlug_Rejects(t *testing.T) {
	bad := []string{
		"",
		"foo",
		"foo/bar/baz",
		"/api",
		"octo/",
		"octo//api",
		"octo/<script>",
		"octo/" + strings.Repeat("a", 100),
	}
	for _, s := range bad {
		if err := validateRepoSlug(s); err == nil {
			t.Errorf("validateRepoSlug(%q) = nil, want error", s)
		}
	}
}

func TestCmdConnect_GithubOpensDashboard(t *testing.T) {
	rec := withRecorder(t)
	t.Setenv("FAAS_TOKEN", "tok")
	t.Setenv("FAAS_API", "https://api.example.test")
	if code := cmdConnect([]string{"github"}); code != 0 {
		t.Fatalf("cmdConnect exit = %d, want 0", code)
	}
	if len(rec.urls) != 1 {
		t.Fatalf("recorded urls = %d, want 1", len(rec.urls))
	}
	want := "https://api.example.test/dashboard/account"
	if rec.urls[0] != want {
		t.Errorf("url = %q, want %q", rec.urls[0], want)
	}
}

func TestCmdConnect_UnknownServiceErrors(t *testing.T) {
	_ = withRecorder(t)
	t.Setenv("FAAS_TOKEN", "tok")
	t.Setenv("FAAS_API", "https://api.example.test")
	if code := cmdConnect([]string{"gitlab"}); code != 1 {
		t.Errorf("cmdConnect exit = %d, want 1", code)
	}
}

func TestCmdConnect_NoArgsPrintsUsage(t *testing.T) {
	_ = withRecorder(t)
	t.Setenv("FAAS_TOKEN", "tok")
	if code := cmdConnect(nil); code != 1 {
		t.Errorf("cmdConnect exit = %d, want 1", code)
	}
}

func TestCmdConnect_FallsBackOnBrowserError(t *testing.T) {
	rec := withRecorder(t)
	rec.err = errBoom
	t.Setenv("FAAS_TOKEN", "tok")
	t.Setenv("FAAS_API", "https://api.example.test")
	// Browser fails: we still print the URL and exit 0 (the URL
	// is the value the customer came for; missing the launch is a
	// soft failure).
	if code := cmdConnect([]string{"github"}); code != 0 {
		t.Fatalf("cmdConnect exit = %d, want 0", code)
	}
	if len(rec.urls) != 1 {
		t.Errorf("recorded urls = %d, want 1", len(rec.urls))
	}
}

// TestCmdConnect_GithubJSONOutput pins the --json branch added in
// Tier A8.2: when jsonOutput is set, cmdConnect emits
// {"url": "...", "service": "github"} instead of opening the
// browser. Mirrors the canonical JSON-shape pattern used by
// commands_registry.go and the new TestCmdBillingPortal_JSONOutput.
func TestCmdConnect_GithubJSONOutput(t *testing.T) {
	rec := withRecorder(t)
	t.Setenv("FAAS_TOKEN", "tok")
	t.Setenv("FAAS_API", "https://api.example.test")

	jsonOutput = true
	defer func() { jsonOutput = false }()

	stdout, restore := captureStdout(t)
	defer restore()
	if code := cmdConnect([]string{"github"}); code != 0 {
		t.Fatalf("cmdConnect github --json = %d, want 0", code)
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("stdout not valid JSON: %v\noutput: %s", err, stdout.String())
	}
	if got["url"] != "https://api.example.test/dashboard/account" {
		t.Errorf("json url = %v, want the dashboard account URL", got["url"])
	}
	if got["service"] != "github" {
		t.Errorf("json service = %v, want \"github\"", got["service"])
	}
	if len(rec.urls) != 0 {
		t.Errorf("--json opened browser %d times; want 0", len(rec.urls))
	}
}

func TestCmdOpen_HitsAppURL(t *testing.T) {
	rec := withRecorder(t)
	t.Setenv("FAAS_TOKEN", "tok")
	t.Setenv("FAAS_API", "https://api.example.test")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/hello" {
			t.Errorf("path = %q, want /v1/apps/hello", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"a-1","slug":"hello","type":"function","runtime":"node22","ram_mb":256,"max_concurrency":2,"idle_timeout_s":60,"status":"active","url":"https://hello.apps.example.test","manifest":{}}`)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := cmdOpen([]string{"hello"}); code != 0 {
		t.Fatalf("cmdOpen exit = %d, want 0", code)
	}
	want := "https://hello.apps.example.test"
	if len(rec.urls) != 1 || rec.urls[0] != want {
		t.Fatalf("urls = %v, want [%q]", rec.urls, want)
	}
}

func TestCmdOpen_DashboardFlagHitsDashboardPage(t *testing.T) {
	rec := withRecorder(t)
	t.Setenv("FAAS_TOKEN", "tok")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"a-1","slug":"hello","type":"function","runtime":"node22","ram_mb":256,"max_concurrency":2,"idle_timeout_s":60,"status":"active","url":"https://hello.apps.example.test","manifest":{}}`)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := cmdOpen([]string{"--dashboard", "hello"}); code != 0 {
		t.Fatalf("cmdOpen exit = %d, want 0", code)
	}
	want := srv.URL + "/dashboard/apps/hello"
	if len(rec.urls) != 1 || rec.urls[0] != want {
		t.Fatalf("urls = %v, want [%q]", rec.urls, want)
	}
}

// wakeStub is a test double for the public gateway that returns the
// cold-wake header (see pkg/wire.WakeHeader) for the first N requests,
// then drops the header to simulate the app warming up. Used to drive
// cmdOpen's probe loop.
type wakeStub struct {
	calls  int32
	coldN  int    // first coldN requests return cold; rest are warm
	probeN *int32 // counts only /probe hits (used by dashboard-skip test)
}

func (w *wakeStub) ServeHTTP(rw http.ResponseWriter, _ *http.Request) {
	if w.probeN != nil {
		atomic.AddInt32(w.probeN, 1)
	}
	n := atomic.AddInt32(&w.calls, 1)
	if int(n) <= w.coldN {
		rw.Header().Set(wire.WakeHeader, wire.ColdWakeValue)
	}
	_, _ = rw.Write([]byte("ok"))
}

func TestCmdOpen_WarmAppOpensImmediately(t *testing.T) {
	rec := withRecorder(t)
	t.Setenv("FAAS_TOKEN", "tok")

	probeCalls := int32(0)
	stub := &wakeStub{coldN: 0, probeN: &probeCalls}
	gw := httptest.NewServer(stub)
	defer gw.Close()

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{"id":"a-1","slug":"hello","type":"function","runtime":"node22","ram_mb":256,"max_concurrency":2,"idle_timeout_s":60,"status":"active","url":%q,"manifest":{}}`, gw.URL))
	}))
	defer apiSrv.Close()
	t.Setenv("FAAS_API", apiSrv.URL)

	var stdout bytes.Buffer
	old := osStdout
	osStdout = &stdout
	defer func() { osStdout = old }()

	if code := cmdOpen([]string{"hello"}); code != 0 {
		t.Fatalf("cmdOpen exit = %d, want 0", code)
	}
	// Warm app → exactly one probe (the initial check) then open.
	if got := atomic.LoadInt32(&probeCalls); got != 1 {
		t.Errorf("probe calls = %d, want 1 (warm app, single probe)", got)
	}
	if len(rec.urls) != 1 || rec.urls[0] != gw.URL {
		t.Fatalf("urls = %v, want [%q]", rec.urls, gw.URL)
	}
	out := stdout.String()
	if !strings.Contains(out, "App is warm — opening.") {
		t.Errorf("missing warm line\nfull: %s", out)
	}
	if strings.Contains(out, "Waking app") {
		t.Errorf("unexpected cold line on warm app\nfull: %s", out)
	}
}

func TestCmdOpen_ColdAppWaitsForWarm(t *testing.T) {
	rec := withRecorder(t)
	t.Setenv("FAAS_TOKEN", "tok")

	probeCalls := int32(0)
	// First 3 probes return cold (cold→cold→cold→warm). Probe loop
	// uses 500 ms sleep between attempts, so the test takes ~1.5 s.
	stub := &wakeStub{coldN: 3, probeN: &probeCalls}
	gw := httptest.NewServer(stub)
	defer gw.Close()

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{"id":"a-1","slug":"hello","type":"function","runtime":"node22","ram_mb":256,"max_concurrency":2,"idle_timeout_s":60,"status":"active","url":%q,"manifest":{}}`, gw.URL))
	}))
	defer apiSrv.Close()
	t.Setenv("FAAS_API", apiSrv.URL)

	var stdout bytes.Buffer
	old := osStdout
	osStdout = &stdout
	defer func() { osStdout = old }()

	if code := cmdOpen([]string{"hello"}); code != 0 {
		t.Fatalf("cmdOpen exit = %d, want 0", code)
	}
	if got := atomic.LoadInt32(&probeCalls); got != 4 {
		t.Errorf("probe calls = %d, want 4 (cold×3 then warm)", got)
	}
	if len(rec.urls) != 1 || rec.urls[0] != gw.URL {
		t.Fatalf("urls = %v, want [%q]", rec.urls, gw.URL)
	}
	out := stdout.String()
	if !strings.Contains(out, "Waking app (cold start) — opening in your browser.") {
		t.Errorf("missing cold line\nfull: %s", out)
	}
}

func TestCmdOpen_ColdAppDeadlineExhausts(t *testing.T) {
	rec := withRecorder(t)
	t.Setenv("FAAS_TOKEN", "tok")

	probeCalls := int32(0)
	// Always cold — cmdOpen waits up to 8 s total then opens anyway.
	stub := &wakeStub{coldN: 1000, probeN: &probeCalls}
	gw := httptest.NewServer(stub)
	defer gw.Close()

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{"id":"a-1","slug":"hello","type":"function","runtime":"node22","ram_mb":256,"max_concurrency":2,"idle_timeout_s":60,"status":"active","url":%q,"manifest":{}}`, gw.URL))
	}))
	defer apiSrv.Close()
	t.Setenv("FAAS_API", apiSrv.URL)

	var stdout bytes.Buffer
	old := osStdout
	osStdout = &stdout
	defer func() { osStdout = old }()

	start := time.Now()
	if code := cmdOpen([]string{"hello"}); code != 0 {
		t.Fatalf("cmdOpen exit = %d, want 0", code)
	}
	elapsed := time.Since(start)
	if elapsed < 7*time.Second || elapsed > 12*time.Second {
		t.Errorf("elapsed = %v, want ~8 s (deadline budget)", elapsed)
	}
	if len(rec.urls) != 1 {
		t.Errorf("browser should still be invoked after deadline; got urls=%v", rec.urls)
	}
}

func TestCmdOpen_DashboardSkipsProbe(t *testing.T) {
	_ = withRecorder(t)
	t.Setenv("FAAS_TOKEN", "tok")

	probeCalls := int32(0)
	stub := &wakeStub{coldN: 0, probeN: &probeCalls}
	gw := httptest.NewServer(stub)
	defer gw.Close()

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{"id":"a-1","slug":"hello","type":"function","runtime":"node22","ram_mb":256,"max_concurrency":2,"idle_timeout_s":60,"status":"active","url":%q,"manifest":{}}`, gw.URL))
	}))
	defer apiSrv.Close()
	t.Setenv("FAAS_API", apiSrv.URL)

	if code := cmdOpen([]string{"--dashboard", "hello"}); code != 0 {
		t.Fatalf("cmdOpen exit = %d, want 0", code)
	}
	if got := atomic.LoadInt32(&probeCalls); got != 0 {
		t.Errorf("probe calls = %d, want 0 (dashboard skips probe)", got)
	}
}

func TestCmdOpen_NoArgsPrintsUsage(t *testing.T) {
	_ = withRecorder(t)
	t.Setenv("FAAS_TOKEN", "tok")
	if code := cmdOpen(nil); code != 1 {
		t.Errorf("cmdOpen exit = %d, want 1", code)
	}
}

func TestCmdDeployRepo_OpensRepoPicker(t *testing.T) {
	rec := withRecorder(t)
	t.Setenv("FAAS_TOKEN", "tok")
	t.Setenv("FAAS_API", "https://api.example.test")
	if code := cmdDeployTarball([]string{"--repo", "octo/api", "--name", "api-app"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if len(rec.urls) != 1 {
		t.Fatalf("urls = %d, want 1", len(rec.urls))
	}
	want := "https://api.example.test/dashboard/connect/repos?app=api-app&repo=octo%2Fapi"
	if rec.urls[0] != want {
		t.Errorf("url = %q, want %q", rec.urls[0], want)
	}
}

func TestCmdDeployRepo_RejectsBadRepoShape(t *testing.T) {
	_ = withRecorder(t)
	t.Setenv("FAAS_TOKEN", "tok")
	if code := cmdDeployTarball([]string{"--repo", "not-a-slug"}); code == 0 {
		t.Fatal("bad repo shape should error")
	}
	if code := cmdDeployTarball([]string{"--repo", "octo/api/extra"}); code == 0 {
		t.Fatal("tri-segment repo should error")
	}
}

// TestCmdDeployTarball_SymlinkRejectedWithBadTarballTitle pins the
// symlink-rejection error title at the CLI dispatch layer. A customer
// who runs `ln -s /etc/passwd source.tar.gz && gregale deploy --tarball
// source.tar.gz` must see "Bad --tarball" (not "Deploy failed"), so
// scripted --json pipelines can jq `.title` and distinguish input-shape
// errors from transport failures. The openCustomerFile guard fires
// inside Client.DeployTarball, so the fake apid never sees a POST to
// /v1/apps/<slug>/deployments. CreateApp still runs ahead of the guard
// (commands2.go:279) and is allowed to hit the fake apid once with a
// 409 (the swallow path at commands2.go:281) — the CreateApp call is
// idempotent and the slug has no security content.
func TestCmdDeployTarball_SymlinkRejectedWithBadTarballTitle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Symlink not supported on Windows")
	}
	stderr, restore := captureStderr(t)
	defer restore()

	// Seed a real file then symlink to it — this is the attack shape.
	dir := t.TempDir()
	real := filepath.Join(dir, "real.tar.gz")
	if err := writeMinimalFile(real); err != nil {
		t.Fatalf("seed real: %v", err)
	}
	link := filepath.Join(dir, "link.tar.gz")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// Fake apid: counts requests per route. CreateApp is allowed to be
	// hit once (commands2.go:279 swallows 409). Anything past that —
	// specifically POST /v1/apps/<slug>/deployments — must NOT be hit.
	var deployHits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/deployments") {
			atomic.AddInt32(&deployHits, 1)
		}
		// 409 on CreateApp matches the swallow-at-409 behaviour;
		// any other request returns 202 so we can see unexpected calls.
		if strings.HasSuffix(r.URL.Path, "/apps") && r.Method == http.MethodPost {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"code":"conflict","status":409}`))
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"d1","status":"pending"}`))
	}))
	defer srv.Close()

	t.Setenv("FAAS_TOKEN", "tok")
	t.Setenv("FAAS_API", srv.URL)

	code := cmdDeployTarball([]string{"--tarball", link, "--name", "sym-link"})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	out := stderr.String()
	if !strings.Contains(out, "Bad --tarball") {
		t.Errorf("stderr missing %q; full stderr:\n%s", "Bad --tarball", out)
	}
	if !strings.Contains(out, "refusing to follow symlink") {
		t.Errorf("stderr missing %q; full stderr:\n%s", "refusing to follow symlink", out)
	}
	if hits := atomic.LoadInt32(&deployHits); hits != 0 {
		t.Errorf("fake apid received %d POST(s) to /deployments; want 0 (guard fired too late)", hits)
	}
}

// errBoom is the launcher-error sentinel used by the fallback tests.
var errBoom = errors.New("simulated opener failure")

// zeroConfigDeployServer returns a fake apid that satisfies the full
// no-flag deploy path: CreateApp → POST deployment → SSE log stream with a
// terminal `live` frame so streamDeployLogs exits 0. It records the multipart
// form fields it saw on the deployment POST via the returned pointers.
func zeroConfigDeployServer(t *testing.T, slug string, gotDockerfile, gotSource *int32, gotRuntime *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/apps" && r.Method == http.MethodPost:
			// New app (200 with body) — the swallow-at-409 path is
			// covered elsewhere; here a clean create is fine.
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: slug})
		case r.URL.Path == "/v1/apps/"+slug+"/deployments" && r.Method == http.MethodPost:
			if err := r.ParseMultipartForm(32 << 20); err != nil {
				t.Errorf("ParseMultipartForm: %v", err)
			}
			if r.FormValue("dockerfile") == "true" {
				atomic.StoreInt32(gotDockerfile, 1)
			}
			if gotRuntime != nil {
				*gotRuntime = r.FormValue("runtime")
			}
			if f, _, err := r.FormFile("source"); err == nil {
				atomic.StoreInt32(gotSource, 1)
				_ = f.Close()
			}
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: "pending", AppID: slug})
		case strings.HasPrefix(r.URL.Path, "/v1/deployments/d1/logs"):
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			_, _ = fmt.Fprint(w, "event: status\ndata: {\"status\":\"live\"}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			<-r.Context().Done()
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
}

// TestCmdDeployTarball_NoFlags_PacksCwd pins issue #313: `gregale deploy` with no
// source flag packs the current directory, detects the framework, and uploads
// it as an App deploy (runtime unset). Uses t.Chdir, so it must NOT run
// t.Parallel().
func TestCmdDeployTarball_NoFlags_PacksCwd(t *testing.T) {
	cwd := t.TempDir()
	// A node project. The app slug is derived from the cwd basename, which is
	// a random temp name; we deploy against whatever deriveName produces.
	if err := os.WriteFile(filepath.Join(cwd, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("seed package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "index.js"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed index.js: %v", err)
	}
	t.Chdir(cwd)
	slug := deriveName()

	var gotDockerfile, gotSource int32
	var gotRuntime string
	srv := zeroConfigDeployServer(t, slug, &gotDockerfile, &gotSource, &gotRuntime)
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdDeployTarball(nil); code != 0 {
		t.Fatalf("cmdDeployTarball(nil) = %d, want 0", code)
	}
	if atomic.LoadInt32(&gotSource) != 1 {
		t.Error("fake apid never received a `source` file part")
	}
	if atomic.LoadInt32(&gotDockerfile) != 0 {
		t.Error("dockerfile flag was set for a node project; want unset")
	}
	if gotRuntime != "" {
		t.Errorf("runtime = %q, want empty (App-type deploy)", gotRuntime)
	}
}

// TestCmdDeployTarball_NoFlags_DockerfileSetsFlag pins that a Dockerfile at the
// cwd root flips the multipart dockerfile field to true. Uses t.Chdir → no
// t.Parallel().
func TestCmdDeployTarball_NoFlags_DockerfileSetsFlag(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "Dockerfile"), []byte("FROM scratch"), 0o644); err != nil {
		t.Fatalf("seed Dockerfile: %v", err)
	}
	t.Chdir(cwd)
	slug := deriveName()

	var gotDockerfile, gotSource int32
	srv := zeroConfigDeployServer(t, slug, &gotDockerfile, &gotSource, nil)
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdDeployTarball(nil); code != 0 {
		t.Fatalf("cmdDeployTarball(nil) = %d, want 0", code)
	}
	if atomic.LoadInt32(&gotDockerfile) != 1 {
		t.Error("dockerfile flag not set for a Dockerfile project")
	}
}

// TestCmdDeployTarball_NoFlags_EmptyCwdFriendlyError pins that a directory with
// no recognisable source fails with exit 1 and a helpful message rather than a
// wall of expected markers.
func TestCmdDeployTarball_NoFlags_EmptyCwdFriendlyError(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("seed README: %v", err)
	}
	t.Chdir(cwd)

	stderr, restore := captureStderr(t)
	defer restore()

	t.Setenv("FAAS_API", "http://127.0.0.1:1") // must never be dialled
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdDeployTarball(nil); code != 1 {
		t.Fatalf("cmdDeployTarball(nil) = %d, want 1", code)
	}
	out := stderr.String()
	if !strings.Contains(out, "No deployable source found") {
		t.Errorf("stderr missing friendly error; got:\n%s", out)
	}
}

// TestCmdDeployTarball_NoFlags_DockerfileAndPackageJson pins precedence when
// both a Dockerfile and a package.json are at the cwd root: dockerfile flag
// must be set (Dockerfile wins, matching pkg/builderd.Detector) AND runtime
// must remain empty (App-type deploy so the server's builderd detects
// authoritatively).
func TestCmdDeployTarball_NoFlags_DockerfileAndPackageJson(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "Dockerfile"), []byte("FROM scratch"), 0o644); err != nil {
		t.Fatalf("seed Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("seed package.json: %v", err)
	}
	t.Chdir(cwd)
	slug := deriveName()

	var gotDockerfile, gotSource int32
	var gotRuntime string
	srv := zeroConfigDeployServer(t, slug, &gotDockerfile, &gotSource, &gotRuntime)
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdDeployTarball(nil); code != 0 {
		t.Fatalf("cmdDeployTarball(nil) = %d, want 0", code)
	}
	if atomic.LoadInt32(&gotDockerfile) != 1 {
		t.Error("dockerfile flag not set when Dockerfile + package.json both present; Dockerfile must win")
	}
	if atomic.LoadInt32(&gotSource) != 1 {
		t.Error("fake apid never received a `source` file part")
	}
	if gotRuntime != "" {
		t.Errorf("runtime = %q, want empty (App-type deploy)", gotRuntime)
	}
}

// TestCmdLogs_GrepSinceLevel pins issue #309's CLI acceptance:
//
//   - The new --grep / --since / --level flags reach the server as
//     query params on /v1/apps/{slug}/logs.
//   - Invalid --level (anything outside info|warn|error) exits 2 with
//     a usage message — no round-trip to the server.
//   - Invalid --since (not RFC3339) exits 2 with a usage message —
//     no round-trip to the server.
//   - Zero-value LogFilter (no flags) leaves the URL untouched,
//     preserving the pre-issue-309 wire contract for existing
//     callers. Move 4 will start to act on these params; the SDK
//     signature is stable across both stubs.
//
// The fake apid emits the same `event: not_implemented` + `event:
// end` frames as the real Move 3 stub (handlers_ext.go::streamAppLogs)
// so the test exercises the actual cmdLogs SSE decoder loop, not a
// parallel one.
func TestCmdLogs_GrepSinceLevel(t *testing.T) {
	// capture stdout via the osStdout package seam (commands3.go:35)
	// so the test stays -race-clean (captureStdout races; memory:
	// `capturestdout-race-under--race.md`).
	var stdout bytes.Buffer
	prevOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = prevOut }()

	t.Run("zero_value_filter_omits_query_params", func(t *testing.T) {
		stdout.Reset()
		var gotQuery url.Values
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.Query()
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("event: not_implemented\ndata: {\"reason\":\"stub\"}\n\n"))
			_, _ = w.Write([]byte("event: end\ndata: {}\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}))
		defer srv.Close()
		t.Setenv("FAAS_API", srv.URL)
		t.Setenv("FAAS_TOKEN", "fp_live_x")
		if code := cmdLogs([]string{"myapp"}); code != 0 {
			t.Fatalf("cmdLogs = %d, want 0", code)
		}
		// No filter flags → no grep/since/level keys in the query,
		// but follow=0 must still be present.
		if gotQuery.Get("grep") != "" {
			t.Errorf("grep = %q, want empty (no --grep flag)", gotQuery.Get("grep"))
		}
		if gotQuery.Get("since") != "" {
			t.Errorf("since = %q, want empty (no --since flag)", gotQuery.Get("since"))
		}
		if gotQuery.Get("level") != "" {
			t.Errorf("level = %q, want empty (no --level flag)", gotQuery.Get("level"))
		}
		if gotQuery.Get("follow") != "0" {
			t.Errorf("follow = %q, want 0", gotQuery.Get("follow"))
		}
	})

	t.Run("all_three_flags_forward", func(t *testing.T) {
		stdout.Reset()
		var gotQuery url.Values
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.Query()
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("event: not_implemented\ndata: {\"reason\":\"stub\"}\n\n"))
			_, _ = w.Write([]byte("event: end\ndata: {}\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}))
		defer srv.Close()
		t.Setenv("FAAS_API", srv.URL)
		t.Setenv("FAAS_TOKEN", "fp_live_x")
		// Flags must precede positional args — Go's flag package
		// stops parsing at the first non-flag argument.
		args := []string{
			"--grep", "ERROR",
			"--since", "2026-07-28T00:00:00Z",
			"--level", "error",
			"myapp",
		}
		if code := cmdLogs(args); code != 0 {
			t.Fatalf("cmdLogs = %d, want 0; stdout=%q", code, stdout.String())
		}
		if got := gotQuery.Get("grep"); got != "ERROR" {
			t.Errorf("grep = %q, want ERROR", got)
		}
		if got := gotQuery.Get("since"); got != "2026-07-28T00:00:00Z" {
			t.Errorf("since = %q, want 2026-07-28T00:00:00Z", got)
		}
		if got := gotQuery.Get("level"); got != "error" {
			t.Errorf("level = %q, want error", got)
		}
	})

	t.Run("invalid_level_exits_2_no_round_trip", func(t *testing.T) {
		stdout.Reset()
		hits := int32(0)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&hits, 1)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("event: end\ndata: {}\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}))
		defer srv.Close()
		t.Setenv("FAAS_API", srv.URL)
		t.Setenv("FAAS_TOKEN", "fp_live_x")
		code := cmdLogs([]string{"--level", "trace", "myapp"})
		if code != 2 {
			t.Errorf("cmdLogs(--level trace) = %d, want 2", code)
		}
		if atomic.LoadInt32(&hits) != 0 {
			t.Errorf("server was hit %d times; validation must short-circuit before HTTP", hits)
		}
	})

	t.Run("invalid_since_exits_2_no_round_trip", func(t *testing.T) {
		stdout.Reset()
		hits := int32(0)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&hits, 1)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("event: end\ndata: {}\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}))
		defer srv.Close()
		t.Setenv("FAAS_API", srv.URL)
		t.Setenv("FAAS_TOKEN", "fp_live_x")
		code := cmdLogs([]string{"--since", "yesterday", "myapp"})
		if code != 2 {
			t.Errorf("cmdLogs(--since yesterday) = %d, want 2", code)
		}
		if atomic.LoadInt32(&hits) != 0 {
			t.Errorf("server was hit %d times; validation must short-circuit before HTTP", hits)
		}
	})

	// Issue #315 (tier-2 DX): `gregale logs tail <slug>` is an alias
	// for `gregale logs <slug> --follow`. Tests pin:
	//   - Dispatch reaches the same SSE pump with follow=1.
	//   - --follow on the alias is rejected with exit 2 (the alias
	//     always follows, so passing the flag signals confusion).
	//   - All other logs flags pass through verbatim.
	//   - No-arg `logs tail` exits 1 with a usage hint.
	t.Run("tail_alias_forces_follow", func(t *testing.T) {
		stdout.Reset()
		var gotQuery url.Values
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.Query()
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("event: end\ndata: {}\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}))
		defer srv.Close()
		t.Setenv("FAAS_API", srv.URL)
		t.Setenv("FAAS_TOKEN", "fp_live_x")
		if code := cmdLogs([]string{"tail", "myapp"}); code != 0 {
			t.Fatalf("cmdLogs(tail myapp) = %d, want 0; stdout=%q", code, stdout.String())
		}
		if got := gotQuery.Get("follow"); got != "1" {
			t.Errorf("follow = %q, want 1 (alias must force --follow)", got)
		}
	})

	t.Run("tail_alias_passes_filters_through", func(t *testing.T) {
		stdout.Reset()
		var gotQuery url.Values
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.Query()
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("event: end\ndata: {}\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}))
		defer srv.Close()
		t.Setenv("FAAS_API", srv.URL)
		t.Setenv("FAAS_TOKEN", "fp_live_x")
		args := []string{
			"tail",
			"--grep", "ERROR",
			"--since", "2026-07-28T00:00:00Z",
			"--level", "error",
			"myapp",
		}
		if code := cmdLogs(args); code != 0 {
			t.Fatalf("cmdLogs(tail … myapp) = %d, want 0; stdout=%q", code, stdout.String())
		}
		if got := gotQuery.Get("follow"); got != "1" {
			t.Errorf("follow = %q, want 1", got)
		}
		if got := gotQuery.Get("grep"); got != "ERROR" {
			t.Errorf("grep = %q, want ERROR", got)
		}
		if got := gotQuery.Get("since"); got != "2026-07-28T00:00:00Z" {
			t.Errorf("since = %q, want 2026-07-28T00:00:00Z", got)
		}
		if got := gotQuery.Get("level"); got != "error" {
			t.Errorf("level = %q, want error", got)
		}
	})

	t.Run("tail_alias_rejects_redundant_follow", func(t *testing.T) {
		stdout.Reset()
		hits := int32(0)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&hits, 1)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("event: end\ndata: {}\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}))
		defer srv.Close()
		t.Setenv("FAAS_API", srv.URL)
		t.Setenv("FAAS_TOKEN", "fp_live_x")
		code := cmdLogs([]string{"tail", "--follow", "myapp"})
		if code != 2 {
			t.Errorf("cmdLogs(tail --follow myapp) = %d, want 2 (redundant flag rejected)", code)
		}
		if atomic.LoadInt32(&hits) != 0 {
			t.Errorf("server was hit %d times; redundant-flag rejection must short-circuit", hits)
		}
	})

	t.Run("tail_alias_invalid_level_short_circuits", func(t *testing.T) {
		stdout.Reset()
		hits := int32(0)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&hits, 1)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("event: end\ndata: {}\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}))
		defer srv.Close()
		t.Setenv("FAAS_API", srv.URL)
		t.Setenv("FAAS_TOKEN", "fp_live_x")
		code := cmdLogs([]string{"tail", "--level", "trace", "myapp"})
		if code != 2 {
			t.Errorf("cmdLogs(tail --level trace myapp) = %d, want 2", code)
		}
		if atomic.LoadInt32(&hits) != 0 {
			t.Errorf("server was hit %d times; validation must short-circuit", hits)
		}
	})

	t.Run("tail_alias_no_args_exits_1", func(t *testing.T) {
		stdout.Reset()
		code := cmdLogs([]string{"tail"})
		if code != 1 {
			t.Errorf("cmdLogs(tail) = %d, want 1 (usage)", code)
		}
	})
}

// TestMapFailureMessage_BuildLimitsDocsLinks pins the docs URLs
// emitted in the build-failure copy (mapFailureMessage's oom and
// timeout branches) to the live docs host. Issue #420 / PR-A:
// the original literals pointed at docs.gregale.example (RFC 2606
// reserved TLD) — a missed rename from PR #458. This test pins the
// post-fix shape so the placeholder can't drift back. We assert via
// strings.Contains (not exact match) because the surrounding copy
// may churn; what matters is the host + path. The third case
// pins the negative — `user_error` should NOT leak the reserved
// TLD or any docs URL at all.
func TestMapFailureMessage_BuildLimitsDocsLinks(t *testing.T) {
	cases := []struct {
		name       string
		errClass   string
		wantSubstr string // empty string = "must NOT contain the reserved TLD"
	}{
		{
			name:       "oom",
			errClass:   "oom",
			wantSubstr: "https://docs.gregale.dev/build/limits#memory",
		},
		{
			name:       "timeout",
			errClass:   "timeout",
			wantSubstr: "https://docs.gregale.dev/build/limits#timeout",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mapFailureMessage(tc.errClass)
			if !strings.Contains(got, tc.wantSubstr) {
				t.Fatalf("mapFailureMessage(%q) = %q; want substring %q", tc.errClass, got, tc.wantSubstr)
			}
			// Negative: never the RFC 2606 reserved TLD.
			if strings.Contains(got, "https://docs.gregale.example") {
				t.Fatalf("mapFailureMessage(%q) leaked the reserved TLD docs.gregale.example: %q", tc.errClass, got)
			}
		})
	}

	// Negative: the `user_error` branch has no docs link at all.
	// Pin so a future copy change can't reintroduce one pointing
	// at the reserved TLD.
	t.Run("user_error_no_docs_link", func(t *testing.T) {
		got := mapFailureMessage("user_error")
		if strings.Contains(got, "https://docs.gregale.example") {
			t.Fatalf("mapFailureMessage(user_error) leaked the reserved TLD docs.gregale.example: %q", got)
		}
	})
}

// TestCmdOpenDocs pins the open docs subcommand (Tier A8.1):
//   - positional slug resolves to /cli/<slug>
//   - --slug flag resolves to /cli/<slug>
//   - empty slug resolves to the docs root (not /cli/app —
//     sanitizeSlugForURL's empty-input fallback is bypassed here)
//   - two positionals is rejected
//   - unknown flag is rejected (via flag.ContinueOnError)
//
// All assertions use --json so the test stays hermetic (no
// browser.Open invocation). Each subtest gets its own captureStdout
// because the helper does not expose a Reset (the buffer is the
// only state, so a fresh capture per case is the cleanest pattern).
func TestCmdOpenDocs(t *testing.T) {
	// Enable --json for the duration of the test. jsonOutput is
	// package-global; without this restore, a follow-on test in
	// the same binary would inherit JSON mode.
	oldJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = oldJSON }()

	cases := []struct {
		name        string
		args        []string
		wantCode    int
		wantURLFrag string
		wantSlug    string
	}{
		{"positional_slug", []string{"apps"}, 0, "/cli/apps", "apps"},
		{"flag_slug", []string{"--slug", "queue"}, 0, "/cli/queue", "queue"},
		// "open docs" with no args at all resolves to the docs
		// root, NOT /cli/app. This is the smoke-test case that
		// catches the bug where sanitizeSlugForURL("") falls back
		// to "app" instead of "".
		{"no_args_resolves_to_root", []string{}, 0, "https://docs.gregale.dev", ""},
		// Two positionals is rejected (the docs subcommand takes
		// at most one positional).
		{"two_positional_rejected", []string{"a", "b"}, 1, "", ""},
		// Unknown flag is rejected with the docs topic in the
		// PrintUsage call.
		{"unknown_flag_rejected", []string{"--nope"}, 1, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, restoreOut := captureStdout(t)
			defer restoreOut()
			code := cmdOpenDocs(tc.args)
			if code != tc.wantCode {
				t.Errorf("cmdOpenDocs(%v) code = %d, want %d", tc.args, code, tc.wantCode)
			}
			if tc.wantCode != 0 {
				// On error, no JSON envelope expected — the
				// function exits via PrintUsage / PrintFail,
				// both of which write to stderr (captured
				// separately if needed).
				return
			}
			var got struct {
				Slug string `json:"slug"`
				URL  string `json:"url"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
				t.Fatalf("decode JSON: %v\noutput: %s", err, stdout.String())
			}
			if got.Slug != tc.wantSlug {
				t.Errorf("slug = %q, want %q", got.Slug, tc.wantSlug)
			}
			if !strings.Contains(got.URL, tc.wantURLFrag) {
				t.Errorf("url = %q, want it to contain %q", got.URL, tc.wantURLFrag)
			}
		})
	}
}

// TestCmdOpenDocs_DispatchesFromCmdOpen pins the wiring: `cmdOpen`
// must route `docs` to cmdOpenDocs and pass the remaining args.
// We invoke cmdOpen directly with the args; the test substitutes
// osStdout (cmdOpenDocs's JSON path) so we can decode the wire
// shape end-to-end.
func TestCmdOpenDocs_DispatchesFromCmdOpen(t *testing.T) {
	oldJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = oldJSON }()

	stdout, restoreOut := captureStdout(t)
	defer restoreOut()

	code := cmdOpen([]string{"docs", "queue"})
	if code != 0 {
		t.Errorf("cmdOpen(docs queue) = %d, want 0", code)
	}
	var got struct {
		Slug string `json:"slug"`
		URL  string `json:"url"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON: %v\noutput: %s", err, stdout.String())
	}
	if got.Slug != "queue" {
		t.Errorf("slug = %q, want %q", got.Slug, "queue")
	}
	if !strings.Contains(got.URL, "/cli/queue") {
		t.Errorf("url = %q, want it to contain /cli/queue", got.URL)
	}
}
