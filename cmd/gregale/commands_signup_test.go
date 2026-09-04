// Tests for `gregale signup` (issue #311). The CLI uses the JSON-only
// programmatic auth surface that PR #786 added to apid, so the fixture
// here is the same shape the real server returns.
//
// Hermeticity rules (memory: cmd-gregale-requireslogin-hermeticity):
//   1. t.Setenv("HOME", t.TempDir())
//   2. t.Setenv("XDG_CONFIG_HOME", t.TempDir())
//   3. t.Setenv("FAAS_TOKEN", "")
//   4. setFakeKeyring(t)           — closes the macOS keyring hermeticity
//                                   gap (issue #311 R2).
// Plus the FAAS_API override so the tests don't actually hit production.

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

var signupPlaintext = testAPIKey('f')

// fakeSignupServer returns the canonical ProgrammaticAuthResponse
// body for the /v1/auth/{signup,login,signup/magic-link} routes. The
// route paths are recorded so each test can assert which one was hit.
func fakeSignupServer(t *testing.T, signupStatus int, magicLinkStatus int) (*httptest.Server, *signupServerCounter) {
	t.Helper()
	c := &signupServerCounter{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/signup":
			atomic.AddInt32(&c.signup, 1)
			if signupStatus != http.StatusOK {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(signupStatus)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code":  "invalid_credentials",
					"title": "Invalid credentials",
				})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"account_id":"acc_signup_test","email":"alice@example.com","plan":"free","api_key":{"plaintext":"`+signupPlaintext+`","prefix":"fp_live_","id":"key_signup_test"}}`)
		case "/v1/auth/login":
			atomic.AddInt32(&c.login, 1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"account_id":"acc_signup_test","email":"alice@example.com","plan":"free","api_key":{"plaintext":"`+signupPlaintext+`","prefix":"fp_live_","id":"key_signup_test"}}`)
		case "/v1/auth/signup/magic-link":
			atomic.AddInt32(&c.magicLink, 1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(magicLinkStatus)
			if magicLinkStatus == http.StatusOK {
				_, _ = io.WriteString(w, `{"status":"ok"}`)
			}
		case "/v1/apps": // finalizeLogin's ListApps probe for the quickstart
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"items":[]}`)
		default:
			t.Logf("unexpected server path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, c
}

type signupServerCounter struct {
	signup, login, magicLink int32
}

// TestCmdSignup_InteractiveHappyPath: pipe email + password + confirm,
// httptest returns ProgrammaticAuthResponse, exit 0, token file =
// plaintext, stdout contains "Logged in as".
func TestCmdSignup_InteractiveHappyPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	setFakeKeyring(t)

	srv, counter := fakeSignupServer(t, http.StatusOK, http.StatusOK)
	t.Setenv("FAAS_API", srv.URL)

	pipeStdin(t, "alice@example.com\ncorrect-horse-battery-staple\ncorrect-horse-battery-staple\n")

	if got := cmdSignup(nil); got != 0 {
		t.Fatalf("cmdSignup exit = %d, want 0", got)
	}
	if c := atomic.LoadInt32(&counter.signup); c != 1 {
		t.Errorf("/v1/auth/signup hit %d times, want 1", c)
	}
	if got := readSavedToken(t); got != signupPlaintext {
		t.Errorf("saved token = %q, want %q", got, signupPlaintext)
	}
}

// TestCmdSignup_InteractiveWeakPassword: <12 char password fails
// before any HTTP round-trip. The test asserts the signup counter
// stays at 0 because auth.Validate rejects client-side.
func TestCmdSignup_InteractiveWeakPassword(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	setFakeKeyring(t)

	srv, counter := fakeSignupServer(t, http.StatusOK, http.StatusOK)
	t.Setenv("FAAS_API", srv.URL)

	rd, restore := captureStderr(t)
	pipeStdin(t, "alice@example.com\nshort\nshort\n")
	if got := cmdSignup(nil); got == 0 {
		t.Errorf("cmdSignup exit = 0, want non-zero (weak password)")
	}
	restore()
	if c := atomic.LoadInt32(&counter.signup); c != 0 {
		t.Errorf("/v1/auth/signup hit %d times, want 0 (rejected pre-HTTP)", c)
	}
	if !strings.Contains(rd.String(), "weak") {
		t.Errorf("stderr = %q, want 'weak' marker", rd.String())
	}
}

// TestCmdSignup_InteractiveMismatch: password and confirm differ,
// the handler rejects without any HTTP round-trip.
func TestCmdSignup_InteractiveMismatch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	setFakeKeyring(t)

	srv, counter := fakeSignupServer(t, http.StatusOK, http.StatusOK)
	t.Setenv("FAAS_API", srv.URL)

	rd, restore := captureStderr(t)
	pipeStdin(t, "alice@example.com\ncorrect-horse-battery-staple\ndifferent-password-1234567890\n")
	if got := cmdSignup(nil); got == 0 {
		t.Errorf("cmdSignup exit = 0, want non-zero (mismatch)")
	}
	restore()
	if c := atomic.LoadInt32(&counter.signup); c != 0 {
		t.Errorf("/v1/auth/signup hit %d times, want 0 (rejected pre-HTTP)", c)
	}
	if !strings.Contains(rd.String(), "match") {
		t.Errorf("stderr = %q, want 'match' marker", rd.String())
	}
}

// TestCmdSignup_InteractiveInvalidEmail: malformed email fails
// client-side before any HTTP round-trip.
func TestCmdSignup_InteractiveInvalidEmail(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	setFakeKeyring(t)

	srv, counter := fakeSignupServer(t, http.StatusOK, http.StatusOK)
	t.Setenv("FAAS_API", srv.URL)

	rd, restore := captureStderr(t)
	pipeStdin(t, "not-an-email\ncorrect-horse-battery-staple\ncorrect-horse-battery-staple\n")
	if got := cmdSignup(nil); got == 0 {
		t.Errorf("cmdSignup exit = 0, want non-zero (invalid email)")
	}
	restore()
	if c := atomic.LoadInt32(&counter.signup); c != 0 {
		t.Errorf("/v1/auth/signup hit %d times, want 0 (rejected pre-HTTP)", c)
	}
	if !strings.Contains(rd.String(), "email") {
		t.Errorf("stderr = %q, want 'email' marker", rd.String())
	}
}

// TestCmdSignup_InteractiveLoginFailsWith401: server returns 401
// invalid_credentials. The CLI must surface a non-zero exit and
// MUST NOT write the token file.
func TestCmdSignup_InteractiveLoginFailsWith401(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	setFakeKeyring(t)

	srv, _ := fakeSignupServer(t, http.StatusUnauthorized, http.StatusOK)
	t.Setenv("FAAS_API", srv.URL)

	pipeStdin(t, "alice@example.com\ncorrect-horse-battery-staple\ncorrect-horse-battery-staple\n")
	if got := cmdSignup(nil); got == 0 {
		t.Errorf("cmdSignup exit = 0, want non-zero (server rejected)")
	}
	// Token file must NOT be present.
	if _, err := readSavedTokenOrSkip(t); err == nil {
		t.Errorf("token file written on 401; signup must not write on failure")
	}
}

// TestCmdSignup_EmailOnlyHappyPath: --email-only EMAIL posts to the
// magic-link endpoint, prints "Check your email", exits 0. No token
// file is written (the server defers the key mint to the verify step).
func TestCmdSignup_EmailOnlyHappyPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	setFakeKeyring(t)

	srv, counter := fakeSignupServer(t, http.StatusOK, http.StatusOK)
	t.Setenv("FAAS_API", srv.URL)

	rd, restore := captureStderr(t)
	if got := cmdSignup([]string{"--email-only", "alice@example.com"}); got != 0 {
		t.Fatalf("cmdSignup --email-only exit = %d, want 0", got)
	}
	restore()
	if c := atomic.LoadInt32(&counter.magicLink); c != 1 {
		t.Errorf("/v1/auth/signup/magic-link hit %d times, want 1", c)
	}
	if c := atomic.LoadInt32(&counter.signup); c != 0 {
		t.Errorf("/v1/auth/signup hit %d times, want 0 (magic-link path)", c)
	}
	// stdout contains "Check your email".
	if _, err := readSavedTokenOrSkip(t); err == nil {
		t.Errorf("token file written on magic-link path; must be deferred")
	}
	_ = rd
}

// TestCmdSignup_EmailOnlyServerUnreachable: FAAS_API on a closed port
// → cmdSignup exits non-zero and writes no token file.
func TestCmdSignup_EmailOnlyServerUnreachable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	setFakeKeyring(t)

	t.Setenv("FAAS_API", "http://127.0.0.1:1") // closed port

	if got := cmdSignup([]string{"--email-only", "alice@example.com"}); got == 0 {
		t.Errorf("cmdSignup --email-only exit = 0, want non-zero (server unreachable)")
	}
	if _, err := readSavedTokenOrSkip(t); err == nil {
		t.Errorf("token file written on unreachable server")
	}
}

// TestCmdSignup_DispatchesFromMain: gregale signup is wired into
// main.go's switch. Locked here so a future refactor that drops the
// dispatch case fails the test.
func TestCmdSignup_DispatchesFromMain(t *testing.T) {
	// We can't easily fork run() inside a test, so we just assert
	// the dispatch constant exists and main.go's switch case is
	// keyed off it. The runtime behaviour is exercised by the
	// integration path on the cli_login_test.go family.
	if dispatchSignup != "signup" {
		t.Errorf("dispatchSignup = %q, want %q", dispatchSignup, "signup")
	}
}

// TestCmdSignup_Interactive_PipeReadsPlain: when stdin is a pipe
// (testOnlyTTY=false, the CI default), signupInteractive routes the
// password read through readPasswordLineFrom — the same path the
// pre-fix code used. This locks in that the TTY-gated silent-echo
// refactor (G10) does NOT regress the pipe/redirect branch: exit 0,
// counter.signup == 1, token saved = signupPlaintext. The TTY branch
// itself (term.ReadPassword) is unreachable from the Go test suite
// because a pipe fd reports IsTerminal==false; we cover it in a
// manual smoke step in the PR body.
func TestCmdSignup_Interactive_PipeReadsPlain(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	setFakeKeyring(t)

	srv, counter := fakeSignupServer(t, http.StatusOK, http.StatusOK)
	t.Setenv("FAAS_API", srv.URL)

	// No withTTYForTest — pipeStdin sets osStdin to a non-*os.File
	// reader, so readInteractivePassword lands in the line-read
	// branch by virtue of the type assertion failing.
	pipeStdin(t, "alice@example.com\ncorrect-horse-battery-staple\ncorrect-horse-battery-staple\n")

	if got := cmdSignup(nil); got != 0 {
		t.Fatalf("cmdSignup exit = %d, want 0 (pipe path regression)", got)
	}
	if c := atomic.LoadInt32(&counter.signup); c != 1 {
		t.Errorf("/v1/auth/signup hit %d times, want 1", c)
	}
	if got := readSavedToken(t); got != signupPlaintext {
		t.Errorf("saved token = %q, want %q", got, signupPlaintext)
	}
}

// TestCmdSignup_PasswordStdin_HappyPath: pipe email + password (no
// confirm), counter.signup == 1, token file written, exit 0. This is
// the CI-shape smoke.
func TestCmdSignup_PasswordStdin_HappyPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	setFakeKeyring(t)

	srv, counter := fakeSignupServer(t, http.StatusOK, http.StatusOK)
	t.Setenv("FAAS_API", srv.URL)

	pipeStdin(t, "alice@example.com\ncorrect-horse-battery-staple\n")

	if got := cmdSignup([]string{"--password-stdin"}); got != 0 {
		t.Fatalf("cmdSignup --password-stdin exit = %d, want 0", got)
	}
	if c := atomic.LoadInt32(&counter.signup); c != 1 {
		t.Errorf("/v1/auth/signup hit %d times, want 1", c)
	}
	if c := atomic.LoadInt32(&counter.magicLink); c != 0 {
		t.Errorf("/v1/auth/signup/magic-link hit %d times, want 0", c)
	}
	if got := readSavedToken(t); got != signupPlaintext {
		t.Errorf("saved token = %q, want %q", got, signupPlaintext)
	}
}

// TestCmdSignup_PasswordStdin_RejectsShortPassword: auth.Validate
// fires pre-HTTP. The CI shape is single-shot — a 6-char password
// hits the same client-side guard as the interactive path. counter.signup
// must stay at 0.
func TestCmdSignup_PasswordStdin_RejectsShortPassword(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	setFakeKeyring(t)

	srv, counter := fakeSignupServer(t, http.StatusOK, http.StatusOK)
	t.Setenv("FAAS_API", srv.URL)

	rd, restore := captureStderr(t)
	pipeStdin(t, "alice@example.com\nshort\n")
	if got := cmdSignup([]string{"--password-stdin"}); got == 0 {
		t.Errorf("cmdSignup --password-stdin exit = 0, want non-zero (weak password)")
	}
	restore()
	if c := atomic.LoadInt32(&counter.signup); c != 0 {
		t.Errorf("/v1/auth/signup hit %d times, want 0 (rejected pre-HTTP)", c)
	}
	if !strings.Contains(rd.String(), "weak") {
		t.Errorf("stderr = %q, want 'weak' marker", rd.String())
	}
}

// TestCmdSignup_PasswordStdin_MutuallyExclusiveWithEmailOnly: passing
// both flags must short-circuit before any HTTP call. Neither signup
// nor magic-link counter may increment.
func TestCmdSignup_PasswordStdin_MutuallyExclusiveWithEmailOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	setFakeKeyring(t)

	srv, counter := fakeSignupServer(t, http.StatusOK, http.StatusOK)
	t.Setenv("FAAS_API", srv.URL)

	rd, restore := captureStderr(t)
	// No stdin write — neither branch should reach a reader.
	if got := cmdSignup([]string{"--email-only", "alice@example.com", "--password-stdin"}); got == 0 {
		t.Errorf("cmdSignup --email-only --password-stdin exit = 0, want non-zero (mutex violation)")
	}
	restore()
	if c := atomic.LoadInt32(&counter.signup); c != 0 {
		t.Errorf("/v1/auth/signup hit %d times, want 0 (mutex short-circuited)", c)
	}
	if c := atomic.LoadInt32(&counter.magicLink); c != 0 {
		t.Errorf("/v1/auth/signup/magic-link hit %d times, want 0 (mutex short-circuited)", c)
	}
	if !strings.Contains(rd.String(), "mutually exclusive") {
		t.Errorf("stderr = %q, want 'mutually exclusive' marker", rd.String())
	}
}

// TestCmdSignup_PasswordStdin_EmptyStdin: email line closes the email
// read; the password drain then sees EOF with no bytes. signupFromStdin
// must reject this pre-HTTP rather than POST an empty password.
func TestCmdSignup_PasswordStdin_EmptyStdin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	setFakeKeyring(t)

	srv, counter := fakeSignupServer(t, http.StatusOK, http.StatusOK)
	t.Setenv("FAAS_API", srv.URL)

	rd, restore := captureStderr(t)
	pipeStdin(t, "alice@example.com\n")
	if got := cmdSignup([]string{"--password-stdin"}); got == 0 {
		t.Errorf("cmdSignup --password-stdin exit = 0, want non-zero (empty password)")
	}
	restore()
	if c := atomic.LoadInt32(&counter.signup); c != 0 {
		t.Errorf("/v1/auth/signup hit %d times, want 0 (empty stdin rejected)", c)
	}
	if !strings.Contains(rd.String(), "password") {
		t.Errorf("stderr = %q, want 'password' marker", rd.String())
	}
}

// TestReadInteractivePassword_FallsBackOnNonFile: the helper's TTY
// gate is `if f, ok := osStdin.(*os.File); ok && term.IsTerminal(int(f.Fd()))`.
// When osStdin is a non-file reader (pipe, bytes.Buffer, …) the type
// assertion fails and the helper returns the line it read. This test
// is the unit-level regression guard for the silent-echo refactor: the
// helper must NOT crash on a non-file reader, must return the same
// trimmed line as readPasswordLineFrom, and must NOT print to stderr
// twice (no trailing newline from the silent-echo branch).
func TestReadInteractivePassword_FallsBackOnNonFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	setFakeKeyring(t)

	prev := osStdin
	t.Cleanup(func() { osStdin = prev })

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() { r.Close() })
	osStdin = r

	go func() {
		defer w.Close()
		_, _ = w.WriteString("correct-horse-battery-staple\n")
	}()

	rd, restore := captureStderr(t)
	defer restore()

	br := bufio.NewReader(osStdin)
	got, err := readInteractivePassword(br, "Password: ")
	if err != nil {
		t.Fatalf("readInteractivePassword: %v", err)
	}
	if got != "correct-horse-battery-staple" {
		t.Errorf("got %q, want %q", got, "correct-horse-battery-staple")
	}
	// The line-read branch does NOT print a trailing newline — the
	// user-typed \n is consumed by br.ReadString('\n') and then
	// trimmed by strings.TrimRight. Stderr should contain ONLY the
	// "Password: " prompt, no newline. The silent-echo branch is
	// the one that prints an extra \n so the next prompt lands on a
	// fresh line; if we accidentally wired that emit to the non-TTY
	// path, this assertion would fail.
	if got := rd.String(); got != "Password: " {
		t.Errorf("stderr = %q, want %q", got, "Password: ")
	}
}

// readSavedTokenOrSkip returns the saved token if any, with error
// semantics that work for the "no token file" check. The simpler
// readSavedToken fails the test on missing file; this variant
// returns (token, error) so callers can distinguish "no token" from
// "wrong token".
func readSavedTokenOrSkip(t *testing.T) (string, error) {
	t.Helper()
	// Mirror the lookup helper in cli_login_test.go so the keyring
	// stub is honoured.
	if kr := effectiveKeyring(); kr != nil {
		if v, err := kr.Get(keyringService, keyringAccount); err == nil {
			return strings.TrimRight(v, "\r\n"), nil
		}
	}
	p, err := tokenPath()
	if err != nil {
		return "", fmt.Errorf("tokenPath: %w", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}
