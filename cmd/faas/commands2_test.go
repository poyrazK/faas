package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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

// wakeStub is a test double for the public gateway that returns
// x-faas-wake: cold for the first N requests, then drops the header
// to simulate the app warming up. Used to drive cmdOpen's probe loop.
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
		rw.Header().Set("x-faas-wake", "cold")
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
// who runs `ln -s /etc/passwd source.tar.gz && faas deploy --tarball
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
