// Tests for cmd/faas command bodies and config helpers. Existing tests cover
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

	if err := saveToken("file-token-xyz"); err != nil {
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

	if got := loadToken(); got != "file-token-xyz" {
		t.Errorf("loadToken (file) = %q, want file-token-xyz", got)
	}
}

func TestSaveToken_TrimsAndAppendsNewline(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := saveToken("  token-with-whitespace  \n"); err != nil {
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
	if string(b) != "token-with-whitespace\n" {
		t.Errorf("file content = %q", b)
	}
}

func TestLoadToken_MissingFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("FAAS_TOKEN", "")
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
	// is "faas". We just assert it returns a non-empty sanitized value.
	got := deriveName()
	if got == "" {
		t.Fatal("deriveName returned empty")
	}
	if strings.ContainsAny(got, " \t\n/\\") {
		t.Errorf("deriveName = %q, contains path/space characters", got)
	}
}

// --- cmdLogin ---------------------------------------------------------------

func TestCmdLogin_NoToken(t *testing.T) {
	if code := cmdLogin(nil); code != 1 {
		t.Errorf("cmdLogin(nil) = %d, want 1", code)
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

	if code := cmdLogin([]string{"--token", "fp_live_x"}); code != 0 {
		t.Fatalf("cmdLogin success = %d, want 0", code)
	}
	// Token must have been persisted.
	p, err := tokenPath()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("token not saved: %v", err)
	}
	if !strings.Contains(string(b), "fp_live_x") {
		t.Errorf("saved token = %q, want contains fp_live_x", b)
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
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	if code := cmdApps(); code != 0 {
		t.Errorf("cmdApps empty = %d, want 0", code)
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
			_, _ = fmt.Fprint(w, "data: {\"line\":\"building...\"}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			_, _ = fmt.Fprint(w, "data: {\"status\":\"live\"}\n\n")
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

func TestCmdDeploy_AppAlreadyExists(t *testing.T) {
	// 409 on CreateApp should be treated as "exists", then Deploy proceeds.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/apps":
			w.WriteHeader(409)
			_ = json.NewEncoder(w).Encode(api.Problem{Status: 409, Code: "exists", Title: "exists", Detail: "exists"})
		case r.URL.Path == "/v1/apps/existing/deployments":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: "pending", AppID: "existing"})
		case strings.HasPrefix(r.URL.Path, "/v1/deployments/d1/logs"):
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)
			_, _ = fmt.Fprint(w, "data: {\"status\":\"live\"}\n\n")
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
				_, _ = fmt.Fprint(w, "data: {\"reason\":\"timeout\"}\n\n")
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
				_, _ = fmt.Fprint(w, "data: {\"reason\":\"timeout\"}\n\n")
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
}

func TestCmdUsage_JSON_IndentedScalar(t *testing.T) {
	resetJSONOutput()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.UsageResponse{
			AppID: "my-app", Requests: 42, MBSeconds: 123456, IncludedGBHours: 5,
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
	if code := cmdUsage(nil); code != 0 {
		t.Fatalf("cmdUsage JSON = %d, want 0", code)
	}
	out := buf.String()
	if !strings.Contains(out, "\n  ") {
		t.Fatalf("expected indented JSON, got %q", out)
	}
	var u api.UsageResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &u); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	if u.Requests != 42 {
		t.Errorf("requests = %d, want 42", u.Requests)
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
	with := APIError{Problem: api.Problem{Title: "T", Detail: "D", DocsURL: "https://docs.x"}}
	if !strings.Contains(with.Error(), "https://docs.x") {
		t.Errorf("with docs: %q should include docs URL", with.Error())
	}
	without := APIError{Problem: api.Problem{Title: "T", Detail: "D"}}
	if strings.Contains(without.Error(), "https://docs.x") {
		t.Errorf("without docs: %q must not include docs URL", without.Error())
	}
}

// TestAPIError_FallbackURLAlwaysThreeLines (issue #64 D2) locks UX §3.3:
// the three-line shape must hold even when the server omits DocsURL.
// Without the per-code fallback, this test fails on the second case
// because the renderer dropped the third line.
func TestAPIError_FallbackURLAlwaysThreeLines(t *testing.T) {
	cases := []struct {
		name string
		code string
		want string // substring the third line should contain
	}{
		{"plan_limit_apps", api.CodePlanLimitApps, docsURLPrefix + "/plan-limit-apps"},
		{"build_undetected", api.CodeBuildUndetected, docsURLPrefix + "/build/detect"},
		{"billing_past_due", api.CodeBillingPastDue, docsURLPrefix + "/billing"},
		{"capacity", api.CodeCapacity, docsURLPrefix + "/capacity"},
		{"unknown_code_falls_back_to_generic", "no_such_code_xyz", docsURLPrefix},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ae := APIError{Problem: api.Problem{
				Title: "Something broke", Detail: "details here", Code: tc.code,
			}}
			got := ae.Error()
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
	ae := APIError{Problem: api.Problem{Title: "T", Detail: "D"}}
	if got, want := len(strings.Split(ae.Error(), "\n")), 2; got != want {
		t.Errorf("empty Code should render %d lines, got %d:\n%s", want, got, ae.Error())
	}
}
