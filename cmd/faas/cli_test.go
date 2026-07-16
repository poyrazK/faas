// Tests for cmd/faas command bodies and config helpers. Existing tests cover
// the dispatcher + a couple of client paths; this file focuses on the parts
// that were at 0%: cmdLogin, cmdLogout, cmdWhoami, cmdApps, cmdDeploy,
// deriveName, apiBase, tokenPath, saveToken, and loadToken.

package main

import (
	"context"
	"encoding/json"
	"errors"
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
	if code := cmdDeploy(nil); code != 1 {
		t.Errorf("cmdDeploy no image = %d, want 1", code)
	}
}

func TestCmdDeploy_UnknownFlag(t *testing.T) {
	if code := cmdDeploy([]string{"--bogus"}); code != 1 {
		t.Errorf("cmdDeploy unknown flag = %d, want 1", code)
	}
}

func TestCmdDeploy_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "a1", Slug: "my-app"})
		case "/v1/apps/my-app/deployments":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: "pending"})
		default:
			http.Error(w, "no", 404)
		}
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	if code := cmdDeploy([]string{"--image", "registry.x/app@sha256:abc", "--name", "my-app"}); code != 0 {
		t.Errorf("cmdDeploy happy = %d, want 0", code)
	}
}

func TestCmdDeploy_AppAlreadyExists(t *testing.T) {
	// 409 on CreateApp should be treated as "exists", then Deploy proceeds.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps":
			w.WriteHeader(409)
			_ = json.NewEncoder(w).Encode(api.Problem{Status: 409, Code: "exists", Title: "exists", Detail: "exists"})
		case "/v1/apps/existing/deployments":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: "pending"})
		default:
			http.Error(w, "no", 404)
		}
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	if code := cmdDeploy([]string{"--image", "registry.x/app@sha256:abc", "--name", "existing"}); code != 0 {
		t.Errorf("cmdDeploy with existing app = %d, want 0", code)
	}
}

// --- printErr / exitCodeForStatus / errAuth ---------------------------------

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
	ec, ok := base.(*exitErr)
	if !ok {
		t.Fatalf("errAuth did not return *exitErr, got %T", base)
	}
	if ec.code != 2 {
		t.Errorf("code = %d, want 2", ec.code)
	}
	if !strings.Contains(ec.msg, "nope") {
		t.Errorf("msg = %q, want contains 'nope'", ec.msg)
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
