// Tests for cmd/gregale command bodies and config helpers. Existing tests cover
// the dispatcher + a couple of client paths; this file focuses on the parts
// that were at 0%: cmdLogin, cmdLogout, cmdWhoami, cmdApps, cmdDeploy,
// deriveName, apiBase, tokenPath, saveToken, and loadToken.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// --- apiBase / tokenPath / saveToken / loadToken ----------------------------

func TestAPIBase_Default(t *testing.T) {
	t.Setenv("FAAS_API", "")
	if got := apiBase(); got != defaultAPIBase {
		t.Errorf("apiBase() = %q, want %q", got, defaultAPIBase)
	}
}

func TestAPIBase_OverrideTrimsTrailingSlash(t *testing.T) {
	t.Setenv("FAAS_API", "https://example.com/")
	if got := apiBase(); got != "https://example.com" {
		t.Errorf("apiBase() = %q, want trailing slash stripped", got)
	}
}

func TestAPIBase_NormalizesBareHost(t *testing.T) {
	t.Setenv("FAAS_API", " api.example.com/// ")
	if got := apiBase(); got != "https://api.example.com" {
		t.Errorf("apiBase() = %q, want https scheme and no trailing slashes", got)
	}
}

func TestAPIBase_PreservesExplicitScheme(t *testing.T) {
	t.Setenv("FAAS_API", "http://127.0.0.1:8081///")
	if got := apiBase(); got != "http://127.0.0.1:8081" {
		t.Errorf("apiBase() = %q, want explicit http scheme preserved", got)
	}
}

func TestTokenPath_UsesUserConfigDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir) // honour on Linux; ignored on macOS where ~/Library/Application Support is used.

	// Force UserConfigDir to our temp dir for this test by routing HOME.
	t.Setenv("HOME", dir)
	// On macOS, UserConfigDir uses ~/Library/Application Support. On Linux, XDG_CONFIG_HOME.
	// Either way, the parent dir of the returned path must be writable.
	p, err := tokenPath()
	if err != nil {
		t.Fatalf("tokenPath: %v", err)
	}
	if !filepath.IsAbs(p) {
		t.Errorf("tokenPath = %q, want absolute", p)
	}
	if filepath.Base(p) != "token" {
		t.Errorf("tokenPath basename = %q, want token", filepath.Base(p))
	}
}

func TestSaveAndLoadToken_EnvOverride(t *testing.T) {
	t.Setenv("FAAS_TOKEN", "env-token-123")
	if got := loadToken(); got != "env-token-123" {
		t.Errorf("loadToken (env) = %q, want env-token-123", got)
	}
}

func TestSaveAndLoadToken_FileRoundTrip(t *testing.T) {
	// Save to a temp config dir.
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("FAAS_TOKEN", "") // ensure we hit the file path
	// Force the file-fallback branch so the file is actually written
	// on hosts where the OS keychain is reachable (issue #293 — the
	// file is now a fallback, not the primary store).
	setFakeKeyring(t, withSetErr(errors.New("keychain unavailable")))
	want := testAPIKey('f')

	if err := saveToken(want); err != nil {
		t.Fatalf("saveToken: %v", err)
	}

	// Permissions on the saved file must be 0600 (secret at rest).
	p, err := tokenPath()
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Errorf("token file perm = %o, want 0o600", got)
	}

	if got := loadToken(); got != want {
		t.Errorf("loadToken (file) = %q, want valid file token", got)
	}
}

func TestSaveToken_TrimsAndAppendsNewline(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	// Force the file-fallback branch (see TestSaveAndLoadToken_FileRoundTrip).
	setFakeKeyring(t, withSetErr(errors.New("keychain unavailable")))
	want := testAPIKey('a')
	if err := saveToken("  " + want + "  \n"); err != nil {
		t.Fatal(err)
	}
	p, err := tokenPath()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != want+"\n" {
		t.Errorf("file content = %q", b)
	}
}

func TestLoadToken_MissingFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("FAAS_TOKEN", "")
	// Empty stub so loadToken's keychain branch returns ErrNotFound
	// and we exercise the missing-file fall-through.
	setFakeKeyring(t)
	if got := loadToken(); got != "" {
		t.Errorf("loadToken with missing file = %q, want empty", got)
	}
}

// --- sanitizeSlug (extra cases) ---------------------------------------------

func TestSanitizeSlug_LengthCapAndPad(t *testing.T) {
	// Inputs that exercise the >=40 truncation and the <3 padding branches.
	cases := map[string]string{
		"":                       "app",                   // all stripped → "app-" then Trim → "app"
		"---":                    "app",                   // all dashes, trimmed to empty, padded, then trimmed
		strings.Repeat("a", 100): strings.Repeat("a", 40), // truncated
		"a":                      "app-a",                 // too short, padded
		"abc":                    "abc",                   // exactly 3, no pad
		"!!!@@@":                 "app",                   // all garbage → "app-" → "app"
	}
	for in, want := range cases {
		if got := sanitizeSlug(in); got != want {
			t.Errorf("sanitizeSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- deriveName -------------------------------------------------------------

func TestDeriveName_UsesCWD(t *testing.T) {
	// deriveName uses os.Getwd; in `go test` that's the package dir which
	// is "gregale". We just assert it returns a non-empty sanitized value.
	got := deriveName()
	if got == "" {
		t.Fatal("deriveName returned empty")
	}
	if strings.ContainsAny(got, " \t\n/\\") {
		t.Errorf("deriveName = %q, contains path/space characters", got)
	}
}

// --- cmdLogin ---------------------------------------------------------------

// TestCmdLogin_NoToken exercises the post-interactive-flow default:
// no flags → enter the device-code path. We can't let it run the
// full flow in a unit test (it would block on stdin), so we point
// FAAS_API at an unreachable port and assert the mint error path
// returns non-zero. This is the "server unreachable" sub-case of the
// interactive flow — see TestCmdLogin_ServerUnreachable for the
// positive assertion.
func TestCmdLogin_NoToken(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	t.Setenv("FAAS_TOKEN", "")

	if code := cmdLogin(nil); code == 0 {
		t.Error("cmdLogin(nil) with unreachable server = 0, want non-zero")
	}
}

func TestCmdLogin_UnknownFlag(t *testing.T) {
	if code := cmdLogin([]string{"--bogus"}); code != 1 {
		t.Errorf("cmdLogin with unknown flag = %d, want 1", code)
	}
}

func TestCmdLogin_BadAPIResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		_ = json.NewEncoder(w).Encode(api.Problem{
			Status: 401, Code: api.CodeUnauthorized,
			Title: "Unauthorized", Detail: "bad token",
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_DIR", dir)
	t.Setenv("FAAS_TOKEN", "")
	// Use a token path under our temp HOME so we don't pollute the real one.
	t.Setenv("XDG_CONFIG_HOME", dir)

	if code := cmdLogin([]string{"--token", "fp_live_x"}); code == 0 {
		t.Error("cmdLogin with bad token should not succeed")
	}
}

func TestCmdLogin_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.AccountResponse{Email: "alice@x.com", Plan: "pro"})
	}))
	defer srv.Close()

	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "")
	setFakeKeyring(t)
	want := testAPIKey('c')

	if code := cmdLogin([]string{"--token", want}); code != 0 {
		t.Fatalf("cmdLogin success = %d, want 0", code)
	}
	// Token must have been persisted. After issue #293 the canonical
	// store is the OS keychain; on a host with a real keychain
	// the file is no longer written. Use loadToken (priority env
	// → keychain → file) so the assertion matches production
	// semantics regardless of which store was written to.
	if got := loadToken(); got != want {
		t.Errorf("loadToken = %q, want login token", got)
	}
}

// --- cmdLogout / cmdWhoami --------------------------------------------------

func TestCmdLogout_AlwaysSucceeds(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	if code := cmdLogout(); code != 0 {
		t.Errorf("cmdLogout = %d, want 0", code)
	}
}

func TestCmdWhoami_Unauthenticated(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("FAAS_TOKEN", "")
	if code := cmdWhoami(); code == 0 {
		t.Error("cmdWhoami without token must fail")
	}
}

func TestCmdWhoami_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.AccountResponse{
			Email: "alice@x.com", Plan: "pro", Status: "active",
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	if code := cmdWhoami(); code != 0 {
		t.Errorf("cmdWhoami = %d, want 0", code)
	}
}

// --- cmdApps ----------------------------------------------------------------

func TestCmdApps_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]api.AppResponse{})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	token := testAPIKey('a')
	t.Setenv("FAAS_TOKEN", token)

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdApps(); code != 0 {
		t.Errorf("cmdApps empty = %d, want 0", code)
	}
	out := stdout.String()
	// Template-friendly nudge (issue #65 D5).
	if !strings.Contains(out, "No apps yet.") {
		t.Errorf("missing 'No apps yet.' line\nfull: %s", out)
	}
	if !strings.Contains(out, "--template hello-node") {
		t.Errorf("missing template hint\nfull: %s", out)
	}
	// The old --image hint must be gone.
	if strings.Contains(out, "--image <ref>") {
		t.Errorf("old --image hint must be replaced\nfull: %s", out)
	}
}

func TestCmdApps_NonEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]api.AppResponse{
			{Slug: "alpha", Status: "active", URL: "https://alpha.apps.x"},
			{Slug: "beta", Status: "evicted_cold", URL: "https://beta.apps.x"},
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	if code := cmdApps(); code != 0 {
		t.Errorf("cmdApps non-empty = %d, want 0", code)
	}
}

func TestCmdApps_Unauthenticated(t *testing.T) {
	t.Setenv("FAAS_TOKEN", "")
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	if code := cmdApps(); code == 0 {
		t.Error("cmdApps without token must fail")
	}
}

// --- cmdDeploy --------------------------------------------------------------

func TestCmdDeploy_NoImage(t *testing.T) {
	// Issue #961 / Mega-A PR-1 added a zero-config branch that packs
	// cwd when it's inside a git repo with a GitHub origin (issue
	// #313 pre-existing). Run the test from a non-git tempdir so the
	// zero-config branch doesn't fire and the "no source flag" path
	// stays the assertion target.
	origCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer func() { _ = os.Chdir(origCwd) }()
	nonGit := t.TempDir()
	if err := os.Chdir(nonGit); err != nil {
		t.Fatalf("Chdir %s: %v", nonGit, err)
	}
	if code := cmdDeployTarball(nil); code != 1 {
		t.Errorf("cmdDeploy no image = %d, want 1", code)
	}
}

func TestCmdDeploy_UnknownFlag(t *testing.T) {
	if code := cmdDeployTarball([]string{"--bogus"}); code != 1 {
		t.Errorf("cmdDeploy unknown flag = %d, want 1", code)
	}
}

func TestCmdDeploy_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/apps":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "my-app"})
		case r.URL.Path == "/v1/apps/my-app/deployments":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: "pending", AppID: "my-app"})
		case strings.HasPrefix(r.URL.Path, "/v1/deployments/d1/logs"):
			// Fake-apid "live" terminal frame so the CLI exits 0.
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			_, _ = fmt.Fprint(w, "event: log\ndata: {\"line\":\"building...\"}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			_, _ = fmt.Fprint(w, "event: status\ndata: {\"status\":\"live\"}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			// Block until the client disconnects so the CLI's stream
			// reader sees the terminal frame before EOF.
			<-r.Context().Done()
		default:
			http.Error(w, "no", 404)
		}
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	if code := cmdDeployTarball([]string{"--image", "registry.x/app@sha256:abc", "--name", "my-app"}); code != 0 {
		t.Errorf("cmdDeploy happy = %d, want 0", code)
	}
}

// TestCmdDeploy_HappyPath_PrintsColdWakeSentence (issue #65 D2) pins
// that the UX §2.5 cold-wake honesty sentence is printed to stdout
// immediately after ✓ Deployed. for the SSE success path.
func TestCmdDeploy_HappyPath_PrintsColdWakeSentence(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/apps":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "my-app"})
		case r.URL.Path == "/v1/apps/my-app/deployments":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: "pending", AppID: "my-app"})
		case strings.HasPrefix(r.URL.Path, "/v1/deployments/d1/logs"):
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			_, _ = fmt.Fprint(w, "event: status\ndata: {\"status\":\"live\"}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			<-r.Context().Done()
		default:
			http.Error(w, "no", 404)
		}
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdDeployTarball([]string{"--image", "registry.x/app@sha256:abc", "--name", "my-app"}); code != 0 {
		t.Fatalf("cmdDeploy exit = %d, want 0", code)
	}
	out := stdout.String()
	for _, want := range []string{
		"Deployed. https://my-app.gregale.dev",
		"scales to zero when idle",
		"~0.3–0.8s to wake",
		"normal and free",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q\nfull: %s", want, out)
		}
	}
}

func TestCmdDeploy_AppAlreadyExists(t *testing.T) {
	// 409 on CreateApp should be treated as "exists", then Deploy proceeds.
	// Issue #1182: after the 409, the CLI issues a GetApp(slug) probe to
	// disambiguate same-account vs other-account. A 200 here signals "ours"
	// and the deploy falls through to the DeployTarball leg.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/apps":
			w.WriteHeader(409)
			_ = json.NewEncoder(w).Encode(api.Problem{Status: 409, Code: "exists", Title: "exists", Detail: "exists"})
		case r.URL.Path == "/v1/apps/existing":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a-existing", Slug: "existing"})
		case r.URL.Path == "/v1/apps/existing/deployments":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: "pending", AppID: "existing"})
		case strings.HasPrefix(r.URL.Path, "/v1/deployments/d1/logs"):
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			_, _ = fmt.Fprint(w, "event: status\ndata: {\"status\":\"live\"}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			<-r.Context().Done()
		default:
			http.Error(w, "no", 404)
		}
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	if code := cmdDeployTarball([]string{"--image", "registry.x/app@sha256:abc", "--name", "existing"}); code != 0 {
		t.Errorf("cmdDeploy with existing app = %d, want 0", code)
	}
}

// --- printErr / exitCodeForStatus / errAuth ---------------------------------

// TestCmdDeploy_JSON_DeployErrorEmitsRFC7807OnStderr (issue #64 D1
// acceptance) ties the two halves together: --json mode for a
// failing deploy must emit the raw RFC 7807 body on stderr, not
// the three-line human render. The fake-apid returns a 404 with
// a problem body so the failure path is real.
func TestCmdDeploy_JSON_DeployErrorEmitsRFC7807OnStderr(t *testing.T) {
	resetJSONOutput()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "my-app"})
		case "/v1/apps/my-app/deployments":
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(api.Problem{
				Status: 404, Code: api.CodeNotFound,
				Title: "App not found", Detail: "no such app",
			})
		default:
			http.Error(w, "no", 404)
		}
	}))
	defer srv.Close()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	prev := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = prev }()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	jsonOutput = true
	defer func() { resetJSONOutput() }()
	code := cmdDeployTarball([]string{"--image", "registry.x/app@sha256:abc", "--name", "my-app"})
	_ = w.Close()
	data, _ := io.ReadAll(r)

	if code != 1 {
		t.Errorf("expected exit 1 (404), got %d", code)
	}
	var p api.Problem
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &p); err != nil {
		t.Fatalf("stderr not valid RFC 7807 JSON: %v\n%s", err, data)
	}
	if p.Code != api.CodeNotFound {
		t.Errorf("code = %q, want %q", p.Code, api.CodeNotFound)
	}
	if p.Status != 404 {
		t.Errorf("status = %d, want 404", p.Status)
	}
}

// TestCmdDeploy_StreamBrokenRecoversViaGetDeployment (F2) pins the
// fallback recovery path: when the SSE log stream emits an `event:
// end` backstop frame (apid's 10-min build timeout, or any other
// premature close) the CLI must do one GetDeployment poll to recover
// the terminal status. A `live` row returns 0; a `failed` row
// returns 1 with the failure-class copy.
func TestCmdDeploy_StreamBrokenRecoversViaGetDeployment(t *testing.T) {
	t.Run("live", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/v1/apps":
				_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "my-app"})
			case r.URL.Path == "/v1/apps/my-app/deployments":
				_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: "pending", AppID: "my-app"})
			case strings.HasPrefix(r.URL.Path, "/v1/deployments/d1/logs"):
				w.Header().Set("Content-Type", "text/event-stream")
				flusher, _ := w.(http.Flusher)
				// No terminal frame; just `event: end`. Forces the
				// CLI to fall back to GetDeployment.
				_, _ = fmt.Fprint(w, "event: end\ndata: {\"reason\":\"timeout\"}\n\n")
				if flusher != nil {
					flusher.Flush()
				}
				<-r.Context().Done()
			case r.URL.Path == "/v1/deployments/d1":
				_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: "live", AppID: "my-app"})
			default:
				http.Error(w, "no", 404)
			}
		}))
		defer srv.Close()

		t.Setenv("FAAS_API", srv.URL)
		t.Setenv("FAAS_TOKEN", "fp_live_x")
		if code := cmdDeployTarball([]string{"--image", "registry.x/app@sha256:abc", "--name", "my-app"}); code != 0 {
			t.Errorf("recovered live = %d, want 0", code)
		}
	})

	t.Run("failed", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/v1/apps":
				_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "my-app"})
			case r.URL.Path == "/v1/apps/my-app/deployments":
				_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: "pending", AppID: "my-app"})
			case strings.HasPrefix(r.URL.Path, "/v1/deployments/d1/logs"):
				w.Header().Set("Content-Type", "text/event-stream")
				flusher, _ := w.(http.Flusher)
				_, _ = fmt.Fprint(w, "event: end\ndata: {\"reason\":\"timeout\"}\n\n")
				if flusher != nil {
					flusher.Flush()
				}
				<-r.Context().Done()
			case r.URL.Path == "/v1/deployments/d1":
				_ = json.NewEncoder(w).Encode(api.DeploymentResponse{
					ID: "d1", Status: "failed", AppID: "my-app", Error: "oom",
				})
			default:
				http.Error(w, "no", 404)
			}
		}))
		defer srv.Close()

		t.Setenv("FAAS_API", srv.URL)
		t.Setenv("FAAS_TOKEN", "fp_live_x")
		if code := cmdDeployTarball([]string{"--image", "registry.x/app@sha256:abc", "--name", "my-app"}); code != 1 {
			t.Errorf("recovered failed/oom = %d, want 1", code)
		}
	})
}

// TestCmdDeploy_StreamOpenFailsRecoversViaGetDeployment covers the
// recovery path when the SSE connection itself can't be opened (DNS,
// proxy, TLS). The fake-apid closes the SSE endpoint without writing
// a single byte, which surfaces as a network error on the client side
// and triggers the GetDeployment retry.
func TestCmdDeploy_StreamOpenFailsRecoversViaGetDeployment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/apps":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "my-app"})
		case r.URL.Path == "/v1/apps/my-app/deployments":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: "pending", AppID: "my-app"})
		case strings.HasPrefix(r.URL.Path, "/v1/deployments/d1/logs"):
			// Hijack the connection and close it without writing a
			// single byte — the CLI sees a network-level EOF on Do().
			hj, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "no hijack", 500)
				return
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			_ = conn.Close()
		case r.URL.Path == "/v1/deployments/d1":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: "live", AppID: "my-app"})
		default:
			http.Error(w, "no", 404)
		}
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	if code := cmdDeployTarball([]string{"--image", "registry.x/app@sha256:abc", "--name", "my-app"}); code != 0 {
		t.Errorf("recovered live after stream-open failure = %d, want 0", code)
	}
}

func TestExitCodeForStatus(t *testing.T) {
	cases := map[int]int{
		200: 1, // unexpected success path; never called here, but default is 1
		401: 2,
		402: 2,
		403: 1,
		404: 1,
		409: 1,
		500: 3,
		503: 3,
	}
	for status, want := range cases {
		if got := exitCodeForStatus(status); got != want {
			t.Errorf("exitCodeForStatus(%d) = %d, want %d", status, got, want)
		}
	}
}

func TestExitErr_Error(t *testing.T) {
	e := &exitErr{msg: "boom", code: 7}
	if e.Error() != "boom" {
		t.Errorf("Error() = %q, want boom", e.Error())
	}
}

func TestErrAuth_PreservesCode(t *testing.T) {
	base := errAuth(errors.New("nope"))
	var ec *exitErr
	if !errors.As(base, &ec) {
		t.Fatalf("errAuth did not return *exitErr, got %T", base)
	}
	if ec.code != 2 {
		t.Errorf("code = %d, want 2", ec.code)
	}
	if !strings.Contains(ec.msg, "nope") {
		t.Errorf("msg = %q, want contains 'nope'", ec.msg)
	}
}

// --- --json mode tests (issue #64 D1) ---------------------------------------

func TestCmdWhoami_JSON(t *testing.T) {
	resetJSONOutput()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.AccountResponse{
			Email: "alice@x.com", Plan: "pro", Status: "active",
		})
	}))
	defer srv.Close()

	var buf bytes.Buffer
	prev := osStdout
	osStdout = &buf
	defer func() { osStdout = prev }()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	jsonOutput = true
	defer func() { resetJSONOutput() }()
	if code := cmdWhoami(); code != 0 {
		t.Fatalf("cmdWhoami JSON = %d, want 0", code)
	}
	var got api.AccountResponse
	if err := json.Unmarshal([]byte(strings.TrimRight(buf.String(), "\n")), &got); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, buf.String())
	}
	if got.Email != "alice@x.com" {
		t.Errorf("email = %q, want alice@x.com", got.Email)
	}
}

func TestCmdApps_JSON_NDJSONShape(t *testing.T) {
	resetJSONOutput()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]api.AppResponse{
			{Slug: "alpha", Status: "live", URL: "https://alpha.x"},
			{Slug: "beta", Status: "parked", URL: "https://beta.x"},
		})
	}))
	defer srv.Close()

	var buf bytes.Buffer
	prev := osStdout
	osStdout = &buf
	defer func() { osStdout = prev }()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	jsonOutput = true
	defer func() { resetJSONOutput() }()
	if code := cmdApps(); code != 0 {
		t.Fatalf("cmdApps JSON = %d, want 0", code)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 NDJSON lines, got %d:\n%s", len(lines), buf.String())
	}
	for i, l := range lines {
		var a api.AppResponse
		if err := json.Unmarshal([]byte(l), &a); err != nil {
			t.Fatalf("line %d not valid JSON: %v\n%s", i, err, l)
		}
	}
}

func TestCmdDeploy_JSON_SkipsStream(t *testing.T) {
	resetJSONOutput()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "my-app"})
		case "/v1/apps/my-app/deployments":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{
				ID: "d1", Status: "pending", AppID: "my-app",
			})
		default:
			http.Error(w, "no", 404)
		}
	}))
	defer srv.Close()

	var buf bytes.Buffer
	prev := osStdout
	osStdout = &buf
	defer func() { osStdout = prev }()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	jsonOutput = true
	defer func() { resetJSONOutput() }()
	if code := cmdDeployTarball([]string{"--image", "registry.x/app@sha256:abc", "--name", "my-app"}); code != 0 {
		t.Fatalf("cmdDeploy JSON = %d, want 0", code)
	}
	out := strings.TrimRight(buf.String(), "\n")
	var dep api.DeploymentResponse
	if err := json.Unmarshal([]byte(out), &dep); err != nil {
		t.Fatalf("expected indented JSON deployment, got %v\n%s", err, out)
	}
	if dep.ID != "d1" {
		t.Errorf("dep.ID = %q, want d1", dep.ID)
	}
	// Issue #1182 §P1 follow-up: receipt must carry app_url
	// (always), and on the image path commit_sha / source_sha256
	// / dirty stay empty (omitempty + zero values). The pin
	// above unmarshals into api.DeploymentResponse and so ignores
	// the extra top-level keys — that's intentional; the second
	// pass asserts key-level shape so a future regression that
	// drops the receipt round-trip is caught here.
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("doc parse: %v\noutput: %s", err, out)
	}
	if got, ok := doc["app_url"].(string); !ok || got != "https://my-app.gregale.dev" {
		t.Errorf("app_url = %v (present=%v), want https://my-app.gregale.dev", doc["app_url"], ok)
	}
	if _, present := doc["commit_sha"]; present {
		t.Errorf("image path must not stamp commit_sha (prov=nil); got %v", doc["commit_sha"])
	}
	if _, present := doc["source_sha256"]; present {
		t.Errorf("image path must not stamp source_sha256 (no source bytes); got %v", doc["source_sha256"])
	}
}

func TestCmdUsage_JSON_NDJSONList(t *testing.T) {
	resetJSONOutput()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// The wire shape is an ARRAY of UsageResponse objects — the
		// OpenAPI spec, server handler, and cross-language SDKs all
		// agree. See memory: getusage-wire-shape-mismatch. The CLI
		// emits NDJSON (one object per line, no indent) to match the
		// rest of the list-style commands (apps, instances, crons,
		// domains, keys, secrets, deployments).
		_ = json.NewEncoder(w).Encode([]api.UsageResponse{{
			AppID: "my-app", Requests: 42, MBSeconds: 123456, IncludedGBHours: 5,
		}})
	}))
	defer srv.Close()

	var buf bytes.Buffer
	prev := osStdout
	osStdout = &buf
	defer func() { osStdout = prev }()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	jsonOutput = true
	defer func() { resetJSONOutput() }()
	if code := cmdUsage(nil); code != 0 {
		t.Fatalf("cmdUsage JSON = %d, want 0", code)
	}
	out := buf.String()
	if strings.Contains(out, "\n  ") {
		t.Errorf("NDJSON must not be indented; got %q", out)
	}
	dec := json.NewDecoder(strings.NewReader(out))
	var rows []api.UsageResponse
	for {
		var row api.UsageResponse
		if err := dec.Decode(&row); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("NDJSON decode: %v\noutput: %s", err, out)
		}
		rows = append(rows, row)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].Requests != 42 {
		t.Errorf("requests = %d, want 42", rows[0].Requests)
	}
}

func TestPrintErr_JSON_EmitsProblemOnStderr(t *testing.T) {
	resetJSONOutput()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	prev := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = prev }()

	jsonOutput = true
	defer func() { resetJSONOutput() }()

	ae := &APIError{Problem: api.Problem{
		Status: 409, Code: api.CodeConflict, Title: "Conflict", Detail: "app exists",
	}}
	code := printErr("Create failed", ae)
	if code != 1 {
		t.Errorf("printErr code = %d, want 1", code)
	}
	_ = w.Close()
	data, _ := io.ReadAll(r)
	var p api.Problem
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &p); err != nil {
		t.Fatalf("stderr not JSON: %v\n%s", err, data)
	}
	if p.Code != api.CodeConflict {
		t.Errorf("code = %q, want %q", p.Code, api.CodeConflict)
	}
}

// --- Client.do coverage -----------------------------------------------------

func TestClient_NonProblemErrorResponse(t *testing.T) {
	// Server returns 500 with a non-JSON body; do() must fall back to
	// "API error: <status>" rather than swallow it.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "fp_live_x")
	_, err := c.ListApps(context.Background())
	if err == nil {
		t.Fatal("expected error on 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q should mention 500", err.Error())
	}
}

func TestAPIError_RenderWithAndWithoutDocs(t *testing.T) {
	// UX §3.3 is now owned by renderAPIError (commands.go), not by
	// the SDK's APIError.Error() — the SDK's Error() is a single-line
	// "<code>: <detail>" suitable for %w chains. The three-line shape
	// is a CLI concern, so the assertion targets renderAPIError.
	with := APIError{Problem: api.Problem{Title: "T", Detail: "D", DocsURL: "https://docs.x"}}
	var withBuf bytes.Buffer
	renderAPIError(&withBuf, &with)
	if !strings.Contains(withBuf.String(), "https://docs.x") {
		t.Errorf("with docs: %q should include docs URL", withBuf.String())
	}
	without := APIError{Problem: api.Problem{Title: "T", Detail: "D"}}
	var withoutBuf bytes.Buffer
	renderAPIError(&withoutBuf, &without)
	if strings.Contains(withoutBuf.String(), "https://docs.x") {
		t.Errorf("without docs: %q must not include docs URL", withoutBuf.String())
	}
}

// TestAPIError_FallbackURLAlwaysThreeLines (issue #64 D2) locks UX §3.3:
// the three-line shape must hold even when the server omits DocsURL.
// The three-line shape is owned by renderAPIError (commands.go); see
// TestAPIError_RenderWithAndWithoutDocs for the SDK-vs-CLI separation.
func TestAPIError_FallbackURLAlwaysThreeLines(t *testing.T) {
	cases := []struct {
		name string
		code string
		want string // substring the third line should contain
	}{
		{"plan_limit_apps", api.CodePlanLimitApps, cliDocsURL},
		{"build_undetected", api.CodeBuildUndetected, deployFromSourceDocsURL},
		{"billing_past_due", api.CodeBillingPastDue, cliDocsURL},
		{"capacity", api.CodeCapacity, cliDocsURL},
		{"unknown_code_falls_back_to_generic", "no_such_code_xyz", docsURLPrefix},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ae := &APIError{Problem: api.Problem{
				Title: "Something broke", Detail: "details here", Code: tc.code,
			}}
			var buf bytes.Buffer
			renderAPIError(&buf, ae)
			// renderAPIError uses RenderTitle + RenderDocsRow, both
			// newline-terminated; the buffer therefore has a trailing
			// newline. Trim it before counting "lines of content".
			got := strings.TrimRight(buf.String(), "\n")
			lines := strings.Split(got, "\n")
			if len(lines) != 3 {
				t.Fatalf("expected 3 lines, got %d:\n%s", len(lines), got)
			}
			if !strings.HasPrefix(lines[2], "  → ") {
				t.Errorf("third line should start with '  → ', got %q", lines[2])
			}
			if !strings.Contains(lines[2], tc.want) {
				t.Errorf("third line should contain %q, got %q", tc.want, lines[2])
			}
		})
	}

	// Empty Code → 2-line fallback (no docs URL to synthesise — preserves
	// today's behavior for malformed problem bodies).
	ae := &APIError{Problem: api.Problem{Title: "T", Detail: "D"}}
	var buf bytes.Buffer
	renderAPIError(&buf, ae)
	if got, want := len(strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")), 2; got != want {
		t.Errorf("empty Code should render %d lines, got %d:\n%s", want, got, buf.String())
	}
}

// TestRenderAPIError_FiveLineShape (error-explanations cluster, spec
// §6.4 amendment 1) locks the extended 5-line shape when the
// server populates Hint / Why / Fix on the Problem. The legacy
// 3-line shape (above) must remain unchanged when those fields
// are empty — adding rows is additive, never subtractive. Each
// new row is gated on a non-empty value so a code the cluster
// didn't catalog renders identically to today.
func TestRenderAPIError_FiveLineShape(t *testing.T) {
	logs := []api.LogExcerpt{
		{Timestamp: "2026-08-18T19:00:00Z", Level: "error", Source: "vm-init", Message: "dial :8080: connection refused"},
	}
	ae := &APIError{Problem: api.Problem{
		Title:        "No process listening on $PORT",
		Detail:       "your app isn't accepting traffic on the port we expect",
		Hint:         "the readiness probe dialed :8080 and got no listener",
		Why:          "your code binds to 127.0.0.1 not 0.0.0.0",
		Fix:          "• bind to 0.0.0.0\n• run `gregale doctor`",
		Code:         api.CodeAppNotListening,
		DocsURL:      docsURLPrefix + "/errors/app-not-listening",
		RelevantLogs: logs,
	}}
	var buf bytes.Buffer
	renderAPIError(&buf, ae)
	got := strings.TrimRight(buf.String(), "\n")
	lines := strings.Split(got, "\n")

	// 7 expected lines:
	//   0: title (✗ No process…)
	//   1: detail  (  your app…)
	//   2: hint    (  💡 hint: …)
	//   3: why     (  why: …)
	//   4: fix     (  → fix: … — multi-line → at least 1 line here)
	//   5: relevant logs (  ┌─ relevant logs ─)
	//   6: relevant log line (  │ 2026-08-18T19:00:00Z error dial…)
	//   7: relevant logs close (  └─)
	//   8: docs    (  → https://…)
	if len(lines) < 8 {
		t.Fatalf("expected ≥ 8 lines (5 + relevant_logs block + docs), got %d:\n%s", len(lines), got)
	}
	if !strings.Contains(lines[2], "hint:") {
		t.Errorf("line 2 should be the hint row, got %q", lines[2])
	}
	if !strings.Contains(lines[3], "why:") {
		t.Errorf("line 3 should be the why row, got %q", lines[3])
	}
	if !strings.Contains(lines[4], "fix:") {
		t.Errorf("line 4 should be the fix row, got %q", lines[4])
	}
	// The Fix field is multi-line ("• bind to 0.0.0.0\n• run ..."),
	// so the relevant logs block starts 2 lines after line[4].
	// Indexes: title=0, detail=1, hint=2, why=3, fix_line1=4,
	//          fix_line2=5, fence=6, log=7, close=8, docs=9.
	if !strings.Contains(lines[6], "relevant logs") {
		t.Errorf("line 6 should be the relevant logs fence, got %q", lines[6])
	}
	if !strings.HasPrefix(lines[8], "  └─") {
		t.Errorf("line 8 should be the relevant logs close marker, got %q", lines[8])
	}
	if !strings.HasPrefix(lines[len(lines)-1], "  → ") {
		t.Errorf("last line should be the docs URL, got %q", lines[len(lines)-1])
	}
}

// TestMapFailureMessage_GoesThroughCatalog locks the post-cluster
// mapFailureProblem entry point: a typed Problem with a
// catalogued code lifts the hint from the whycopy catalog, not
// the legacy 4-bucket switch.
func TestMapFailureMessage_GoesThroughCatalog(t *testing.T) {
	p := api.NewProblem(422, api.CodeAppLoopbackBound, "constructor-title", "constructor-detail")
	got := mapFailureProblem(p)
	if got == "" {
		t.Fatal("mapFailureProblem on a catalogued code returned empty string (catalog lookup must succeed)")
	}
	// Catalog's Hint contains "127.0.0.1" — the literal loopback
	// address that the customer needs to see in the failure
	// message. The legacy 4-bucket switch never had this string,
	// so it's the right anchor for "this came from the catalog".
	wantSubstr := "127.0.0.1"
	if !strings.Contains(got, wantSubstr) {
		t.Errorf("mapFailureProblem for app_loopback_bound: got %q, want substring %q", got, wantSubstr)
	}
}

// TestRenderAPIError_TTYGatedGlyph locks UX §3.2's interaction with
// §3.3's three-line contract: the static *APIError.Error() always carries
// "  → <URL>", but the renderer (renderAPIError) drops the leading "✗"
// and the docs-row "→" glyph when stdout is not a TTY. The line COUNT
// is identical either way — only the glyphs change — so script consumers
// that split on "\n" still see the same shape. Subtests cover both
// branches; the TTY hook (testOnlyTTY in output.go) makes the result
// deterministic regardless of how `go test` is invoked.
func TestRenderAPIError_TTYGatedGlyph(t *testing.T) {
	ae := &APIError{Problem: api.Problem{
		Title: "Plan limit reached", Detail: "scale=2", DocsURL: "https://docs.x/limit",
	}}
	for _, tc := range []struct {
		name      string
		tty       bool
		wantFirst string // prefix of line 0
		wantThird string // prefix of line 2
	}{
		{"tty_keeps_glyphs", true, "✗ ", "  → "},
		{"non_tty_drops_glyphs", false, "Plan limit", "  https://"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prev := testOnlyTTY
			defer func() { testOnlyTTY = prev }()
			hook := tc.tty
			testOnlyTTY = &hook
			var buf bytes.Buffer
			renderAPIError(&buf, ae)
			// renderAPIError emits a trailing newline, so Split produces
			// 4 elements; trim the trailing empty before counting content lines.
			lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
			if len(lines) != 3 {
				t.Fatalf("want 3 lines, got %d:\n%s", len(lines), buf.String())
			}
			if !strings.HasPrefix(lines[0], tc.wantFirst) {
				t.Errorf("line 0 prefix = %q, want %q", lines[0], tc.wantFirst)
			}
			if !strings.HasPrefix(lines[2], tc.wantThird) {
				t.Errorf("line 2 prefix = %q, want %q", lines[2], tc.wantThird)
			}
		})
	}
}

// --- cmdApp --min N cost echo (issue #65 D3) --------------------------------

// TestCmdApp_Min1_EchoesResidentCost pins the legacy flag form
// `gregale app <slug> --min 1` echoes the same always-resident cost as
// the subcommand form. Pro plan, 512 MB, min=1 → ~15.2 GB-h/mo.
func TestCmdApp_Min1_EchoesResidentCost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/account":
			_ = json.NewEncoder(w).Encode(api.AccountResponse{Email: "jane@x.com", Plan: "pro"})
		case "/v1/apps/jane-api":
			_ = json.NewEncoder(w).Encode(api.AppResponse{Slug: "jane-api", RAMMB: 512, MinInstances: 1})
		default:
			http.Error(w, "no", 404)
		}
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdApp([]string{"jane-api", "--min", "1"}); code != 0 {
		t.Fatalf("cmdApp exit = %d, want 0", code)
	}
	out := stdout.String()
	for _, want := range []string{
		"✓ Updated",
		"1 instance of 512 MB kept warm",
		"~15.2 GB-h/mo",
		"1000 millicent/GB-h overage",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q\nfull: %s", want, out)
		}
	}
}

// TestCmdApp_Min0_NoEcho pins that the legacy flag form is silent
// on --min 0.
func TestCmdApp_Min0_NoEcho(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/account":
			_ = json.NewEncoder(w).Encode(api.AccountResponse{Plan: "pro"})
		case "/v1/apps/jane-api":
			_ = json.NewEncoder(w).Encode(api.AppResponse{Slug: "jane-api", RAMMB: 512, MinInstances: 0})
		default:
			http.Error(w, "no", 404)
		}
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdApp([]string{"jane-api", "--min", "0"}); code != 0 {
		t.Fatalf("cmdApp exit = %d, want 0", code)
	}
	out := stdout.String()
	if strings.Contains(out, "kept warm") {
		t.Errorf("min=0 should not echo cost; got %q", out)
	}
}

// TestCmdApp_Min1_Hobby_NoEcho pins that on Free/Hobby the echo
// never fires (the API rejects with CodePlanMinInstancesNotAllowed
// before the helper would be called).
func TestCmdApp_Min1_Hobby_NoEcho(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/account":
			_ = json.NewEncoder(w).Encode(api.AccountResponse{Plan: "hobby"})
		case "/v1/apps/jane-api":
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(api.Problem{
				Status: 403, Code: "plan_min_instances_not_allowed",
				Title:  "Plan does not allow min_instances > 0",
				Detail: "Hobby plans cannot keep instances warm.",
			})
		default:
			http.Error(w, "no", 404)
		}
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdApp([]string{"jane-api", "--min", "1"}); code != 1 {
		// printErr returns 1 for user-facing rejections (Plan limits → exit 1 per UX §3.2).
		t.Fatalf("cmdApp exit = %d, want 1 (Hobby rejected)", code)
	}
	if strings.Contains(stdout.String(), "kept warm") {
		t.Errorf("Hobby plan must not echo cost; got %q", stdout.String())
	}
}

// --- cmdLogin first-run quickstart (issue #65 D4) ---------------------------

// TestCmdLogin_FirstRun_PrintsQuickstart pins that a fresh account
// (ListApps returns []) sees the 3-line UX §8 quickstart after the
// success line. Failure to list apps must NOT block login (silent).
func TestCmdLogin_FirstRun_PrintsQuickstart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/account":
			_ = json.NewEncoder(w).Encode(api.AccountResponse{Email: "jane@x.com", Plan: "pro"})
		case "/v1/apps":
			_ = json.NewEncoder(w).Encode([]api.AppResponse{})
		default:
			http.Error(w, "no", 404)
		}
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	token := testAPIKey('a')
	t.Setenv("FAAS_TOKEN", token)

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdLogin([]string{"--token", token}); code != 0 {
		t.Fatalf("cmdLogin exit = %d, want 0", code)
	}
	out := stdout.String()
	for _, want := range []string{
		"✓ Logged in as jane@x.com (pro plan)",
		"You're in. Next step",
		"gregale deploy --template hello-node",
		"gregale deploy --tarball",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q\nfull: %s", want, out)
		}
	}
}

// TestCmdLogin_ExistingAccount_NoQuickstart pins that an account
// with at least one app gets the success line but no quickstart.
func TestCmdLogin_ExistingAccount_NoQuickstart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/account":
			_ = json.NewEncoder(w).Encode(api.AccountResponse{Email: "jane@x.com", Plan: "pro"})
		case "/v1/apps":
			_ = json.NewEncoder(w).Encode([]api.AppResponse{{Slug: "jane-api"}})
		default:
			http.Error(w, "no", 404)
		}
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	token := testAPIKey('b')
	t.Setenv("FAAS_TOKEN", token)

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdLogin([]string{"--token", token}); code != 0 {
		t.Fatalf("cmdLogin exit = %d, want 0", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "✓ Logged in as jane@x.com") {
		t.Errorf("missing success line\nfull: %s", out)
	}
	if strings.Contains(out, "You're in. Next step") {
		t.Errorf("existing account must not see quickstart\nfull: %s", out)
	}
}

// TestCmdLogin_ListAppsFails_NoQuickstart pins that a failing ListApps
// call does NOT block login (UX §8: don't gate login on transient
// API issues). The success line still prints.
func TestCmdLogin_ListAppsFails_NoQuickstart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/account":
			_ = json.NewEncoder(w).Encode(api.AccountResponse{Email: "jane@x.com", Plan: "pro"})
		case "/v1/apps":
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			http.Error(w, "no", 404)
		}
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	token := testAPIKey('c')
	t.Setenv("FAAS_TOKEN", token)

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdLogin([]string{"--token", token}); code != 0 {
		t.Fatalf("cmdLogin exit = %d, want 0 (ListApps failure must not block)", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "✓ Logged in as jane@x.com") {
		t.Errorf("missing success line\nfull: %s", out)
	}
	if strings.Contains(out, "You're in. Next step") {
		t.Errorf("ListApps failure must not trigger quickstart\nfull: %s", out)
	}
}

// TestCmdDeploy_Recovery_PrintsColdWakeSentence (issue #65 D2 polish)
// pins the polled-recovery path: when the SSE stream emits `event:
// end` (or any non-terminal frame) and terminalExitForDeployment
// renders the live outcome via GetDeployment, the cold-wake sentence
// must still print. The SSE-path coverage
// (TestCmdDeploy_HappyPath_PrintsColdWakeSentence) only exercises
// streamDeployLogs's live branch; this one exercises the recovery
// branch in terminalExitForDeployment so a future refactor can't
// accidentally drop the sentence from one of the two render sites.
func TestCmdDeploy_Recovery_PrintsColdWakeSentence(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/apps":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "my-app"})
		case r.URL.Path == "/v1/apps/my-app/deployments":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: "pending", AppID: "my-app"})
		case strings.HasPrefix(r.URL.Path, "/v1/deployments/d1/logs"):
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			// No terminal frame; `event: end` forces the CLI to
			// poll GetDeployment (terminalExitForDeployment path).
			_, _ = fmt.Fprint(w, "event: end\ndata: {\"reason\":\"timeout\"}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			<-r.Context().Done()
		case r.URL.Path == "/v1/deployments/d1":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: "live", AppID: "my-app"})
		default:
			http.Error(w, "no", 404)
		}
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdDeployTarball([]string{"--image", "registry.x/app@sha256:abc", "--name", "my-app"}); code != 0 {
		t.Fatalf("cmdDeploy recovery exit = %d, want 0", code)
	}
	out := stdout.String()
	for _, want := range []string{
		"Deployed. https://my-app.gregale.dev",
		"scales to zero when idle",
		"~0.3–0.8s to wake",
		"normal and free",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("recovery stdout missing %q\nfull: %s", want, out)
		}
	}
}

// TestCmdDeploy_RequireAuthn_CarryThrough pins that
// `gregale deploy --image X --require-authn` carries
// `require_authn: true` on the POST /v1/apps body (issue #560
// AC #1 — the customer's CLI round-trip should opt in at deploy
// time so the resulting app returns 401 without a token). Mirrors
// the cmdApp / cmdAppScale Visit-flag pattern at commands2.go:263
// and commands5_test.go:927 (TestCmdAppScale_RequireAuthnTrue)
// but on the deploy path, which is the issue's primary UX entry.
//
// The fake server only stubs the routes cmdDeployTarball touches
// (POST /v1/apps, POST /v1/apps/<slug>/deployments, the SSE logs
// stream) and asserts the CreateApp body carries the explicit
// pointer-to-true. A nil pointer (unset) would have left the
// server default false — that's the "no flag" baseline that TestCmdDeploy_HappyPath
// covers; this test pins the *flag was passed* path.
func TestCmdDeploy_RequireAuthn_CarryThrough(t *testing.T) {
	var gotCreateBody api.CreateAppRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/apps" && r.Method == "POST":
			if err := json.NewDecoder(r.Body).Decode(&gotCreateBody); err != nil {
				http.Error(w, "bad json", 400)
				return
			}
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "authn-app", RequireAuthn: true})
		case r.URL.Path == "/v1/apps/authn-app/deployments":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: "pending", AppID: "authn-app"})
		case strings.HasPrefix(r.URL.Path, "/v1/deployments/d1/logs"):
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			_, _ = fmt.Fprint(w, "event: status\ndata: {\"status\":\"live\"}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			<-r.Context().Done()
		default:
			http.Error(w, "no", 404)
		}
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	if code := cmdDeployTarball([]string{
		"--image", "registry.x/app@sha256:abc",
		"--name", "authn-app",
		"--require-authn",
	}); code != 0 {
		t.Fatalf("cmdDeploy --require-authn exit = %d, want 0", code)
	}
	if gotCreateBody.RequireAuthn == nil {
		t.Fatalf("CreateApp body missing RequireAuthn pointer; want pointer to true")
	}
	if *gotCreateBody.RequireAuthn != true {
		t.Errorf("CreateApp RequireAuthn = %t, want true (deploy --require-authn UX)", *gotCreateBody.RequireAuthn)
	}
}

// TestCmdDeploy_RequireAuthn_Mutex pins that --require-authn and
// --no-require-authn together is a usage error, matching the same
// mutex at cmdApp / cmdAppScale. Returns exit 1 BEFORE any HTTP
// call (asserted: server handler panics are not hit, since the
// server is replaced with a defensive 500 that would clearly
// fingerprint the regression).
func TestCmdDeploy_RequireAuthn_Mutex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server should not be called when mutex flags trip; got %s %s", r.Method, r.URL.Path)
		http.Error(w, "no", 500)
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	if code := cmdDeployTarball([]string{
		"--image", "registry.x/app@sha256:abc",
		"--name", "mutex-app",
		"--require-authn",
		"--no-require-authn",
	}); code != 1 {
		t.Errorf("cmdDeploy mutex exit = %d, want 1 (usage error)", code)
	}
}

// TestCmdDeploy_RequireAuthn_ExistingAppPATCH pins that when the
// app already exists (CreateApp returns 409) and the customer
// passed --require-authn on the deploy, the deploy path PATCHes
// the existing app to flip the flag. This is the second half of
// the AC #1 UX: opt-in works whether the slug is new (POST) or
// already deployed (POST 409 → PATCH). Plan gate (Pro/Scale only)
// still fires server-side at the apid PATCH validator — the fake
// server here just stamps the gate as "passed" and the test
// checks the wire body.
func TestCmdDeploy_RequireAuthn_ExistingAppPATCH(t *testing.T) {
	var gotPatchBody api.UpdateAppRequest
	var sawPatch, sawCreate bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/apps" && r.Method == "POST":
			sawCreate = true
			// Project envelope: the actual codebase returns a 409
			// with a Problem body (api.NewProblem 409). The CLI
			// turns it into an APIError via errors.As in printErr;
			// without the right shape the swallow wouldn't fire
			// and the deploy path would surface "Could not
			// create app" instead of falling through to the
			// PATCH branch.
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(409)
			_ = json.NewEncoder(w).Encode(api.Problem{Status: 409, Code: api.CodeConflict, Title: "Conflict", Detail: "app exists"})
		case r.URL.Path == "/v1/apps/existing-app" && r.Method == "GET":
			// Issue #1182: hybrid slug-conflict probe — after the 409
			// the CLI issues GetApp to disambiguate same-account vs
			// other-account. 200 here means "ours", so the PATCH
			// leg below runs to mirror --require-authn.
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a-existing", Slug: "existing-app"})
		case r.URL.Path == "/v1/apps/existing-app" && r.Method == "PATCH":
			sawPatch = true
			if err := json.NewDecoder(r.Body).Decode(&gotPatchBody); err != nil {
				http.Error(w, "bad json", 400)
				return
			}
			_ = json.NewEncoder(w).Encode(api.AppResponse{Slug: "existing-app", RequireAuthn: true})
		case strings.HasPrefix(r.URL.Path, "/v1/deployments/d1/logs"):
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			_, _ = fmt.Fprint(w, "event: status\ndata: {\"status\":\"live\"}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			<-r.Context().Done()
		case r.URL.Path == "/v1/apps/existing-app/deployments":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: "pending", AppID: "existing-app"})
		default:
			http.Error(w, "no", 404)
		}
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	if code := cmdDeployTarball([]string{
		"--image", "registry.x/app@sha256:abc",
		"--name", "existing-app",
		"--require-authn",
	}); code != 0 {
		t.Fatalf("cmdDeploy existing-app exit = %d, want 0", code)
	}
	if !sawCreate {
		t.Errorf("expected POST /v1/apps (got 409), but server never saw it")
	}
	if !sawPatch {
		t.Errorf("expected PATCH /v1/apps/existing-app after 409 to mirror --require-authn onto existing app; got nothing")
	}
	if gotPatchBody.RequireAuthn == nil || *gotPatchBody.RequireAuthn != true {
		t.Errorf("PATCH body RequireAuthn = %v, want pointer to true", gotPatchBody.RequireAuthn)
	}
}
