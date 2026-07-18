package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

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
		})
	}
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

// errBoom is the launcher-error sentinel used by the fallback tests.
var errBoom = errors.New("simulated opener failure")
