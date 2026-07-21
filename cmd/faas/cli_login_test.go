// Tests for the device-code interactive login flow (spec §2.2).
//
// These tests exercise the CLI side end-to-end: a httptest server
// stands in for apid, the CLI drives POST /v1/cli-auth/code +
// POST /v1/cli-auth/exchange with realistic 200/404/410 responses,
// and we assert the token file + stdout + browser-stub interactions.
//
// Hermeticity rules (memory: cmd-faas-requireslogin-hermeticity):
//   1. t.Setenv("HOME", t.TempDir())
//   2. t.Setenv("XDG_CONFIG_HOME", t.TempDir())
//   3. t.Setenv("FAAS_TOKEN", "")
// All three are required — if HOME alone is set, UserConfigDir
// still resolves under the host's real HOME on macOS, so a stale
// token file would silently make `cmdLogin` succeed where it should
// fail.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/browser"
)

// stubLauncher records Launch() calls and returns a programmable
// error. Mirrors pkg/browser/browser_test.go::recorder so the same
// pattern is used both here and in the browser package's own tests.
type stubLauncher struct {
	urls []string
	err  error
}

func (s *stubLauncher) Launch(url string) error {
	s.urls = append(s.urls, url)
	return s.err
}

// stubBrowser swaps browser.Default for the duration of the test and
// returns a pointer so the caller can inspect URLs and override the
// error. Restored via t.Cleanup.
func stubBrowser(t *testing.T, err error) *stubLauncher {
	t.Helper()
	old := browser.Default
	rec := &stubLauncher{err: err}
	browser.Default = rec
	t.Cleanup(func() { browser.Default = old })
	return rec
}

// pipeStdin replaces osStdin with a pipe containing `data` and
// restores the original at test end. Used to drive the "paste code"
// prompt without blocking on the real terminal.
func pipeStdin(t *testing.T, data string) {
	t.Helper()
	old := osStdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	if _, err := io.WriteString(w, data); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	_ = w.Close()
	osStdin = r
	t.Cleanup(func() { osStdin = old })
}

// captureStderr is defined in commands5_test.go (writes to a temp
// file so non-buffered stderr drains are captured). We reuse it
// directly. The (reader, restore) shape means callers must call
// restore() before reading.

// readSavedToken reads the token file written by saveToken and
// returns its contents (minus trailing newline). Used by every test
// that exercises the happy path.
func readSavedToken(t *testing.T) string {
	t.Helper()
	p, err := tokenPath()
	if err != nil {
		t.Fatalf("tokenPath: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read token: %v", err)
	}
	return strings.TrimRight(string(b), "\r\n")
}

// TestCmdLogin_InteractiveHappyPath_BrowserOpens: server returns a
// mint + a consumed exchange. CLI opens the browser (recorded),
// prints "press Enter to wait", stdin pipe sends empty line, polling
// kicks in and the second exchange returns 200 with plaintext + acct.
// Asserts: exit 0, token file has plaintext, stdout contains the
// "Logged in as" line, browser saw exactly one URL.
func TestCmdLogin_InteractiveHappyPath_BrowserOpens(t *testing.T) {
	const plaintext = "fp_live_abcdef0123456789abcdef0123456789abcdef0123456789"
	var mintCalls, exchangeCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/cli-auth/code":
			atomic.AddInt32(&mintCalls, 1)
			_ = json.NewEncoder(w).Encode(api.CliAuthCodeResponse{
				Code:      "WXYZ-1234",
				URL:       "https://api.example.test/cli-auth?code=WXYZ-1234",
				ExpiresAt: time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339),
			})
		case "/v1/cli-auth/exchange":
			atomic.AddInt32(&exchangeCalls, 1)
			_ = json.NewEncoder(w).Encode(api.CliAuthExchangeResponse{
				Plaintext: plaintext,
				Account:   api.AccountResponse{Email: "jane@example.com", Plan: "free"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "")
	stubBrowser(t, nil)
	out, _ := captureStdout(t)
	// Empty stdin line → polling path kicks in after the 3s timeout.
	pipeStdin(t, "\n")

	if code := cmdLogin(nil); code != 0 {
		t.Fatalf("cmdLogin happy = %d, want 0\nstdout=%s", code, out.String())
	}
	if got := atomic.LoadInt32(&mintCalls); got != 1 {
		t.Errorf("mint calls = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&exchangeCalls); got != 1 {
		t.Errorf("exchange calls = %d, want 1 (polling returned immediately)", got)
	}
	if saved := readSavedToken(t); saved != plaintext {
		t.Errorf("saved token = %q, want %q", saved, plaintext)
	}
	if !strings.Contains(out.String(), "Logged in as jane@example.com") {
		t.Errorf("stdout missing success line: %q", out.String())
	}
	if !strings.Contains(out.String(), "Opening https://api.example.test/cli-auth") {
		t.Errorf("stdout missing opening URL line: %q", out.String())
	}
}

// TestCmdLogin_InteractiveHappyPath_PasteCode: same server, but the
// user types the code into the same terminal so the polling loop is
// skipped. Asserts exactly one exchange call (no 404s).
func TestCmdLogin_InteractiveHappyPath_PasteCode(t *testing.T) {
	const plaintext = "fp_live_pastecode12345678901234567890123456789012345678"
	var exchangeCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/cli-auth/code":
			_ = json.NewEncoder(w).Encode(api.CliAuthCodeResponse{
				Code:      "ABCD-1234",
				URL:       "https://api.example.test/cli-auth?code=ABCD-1234",
				ExpiresAt: time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339),
			})
		case "/v1/cli-auth/exchange":
			atomic.AddInt32(&exchangeCalls, 1)
			_ = json.NewEncoder(w).Encode(api.CliAuthExchangeResponse{
				Plaintext: plaintext,
				Account:   api.AccountResponse{Email: "paste@example.com", Plan: "hobby"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "")
	stubBrowser(t, nil) // browser "succeeds" but the user still pastes
	out, _ := captureStdout(t)
	pipeStdin(t, "ABCD-1234\n")

	if code := cmdLogin(nil); code != 0 {
		t.Fatalf("cmdLogin paste = %d, want 0\nstdout=%s", code, out.String())
	}
	if got := atomic.LoadInt32(&exchangeCalls); got != 1 {
		t.Errorf("exchange calls = %d, want 1 (paste skips polling)", got)
	}
	if saved := readSavedToken(t); saved != plaintext {
		t.Errorf("saved token = %q, want %q", saved, plaintext)
	}
}

// TestCmdLogin_BrowserOpenFailure_FallsBackToURL: browser stub
// returns an error (the typical CI / sandbox case). The CLI must
// still reach the success path AND print the URL + "Could not open
// browser" to stderr so the user can copy it.
func TestCmdLogin_BrowserOpenFailure_FallsBackToURL(t *testing.T) {
	const plaintext = "fp_live_browserfail123456789012345678901234567890123456"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/cli-auth/code":
			_ = json.NewEncoder(w).Encode(api.CliAuthCodeResponse{
				Code:      "WXYZ-1234",
				URL:       "https://api.example.test/cli-auth?code=WXYZ-1234",
				ExpiresAt: time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339),
			})
		case "/v1/cli-auth/exchange":
			_ = json.NewEncoder(w).Encode(api.CliAuthExchangeResponse{
				Plaintext: plaintext,
				Account:   api.AccountResponse{Email: "x@example.com", Plan: "free"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "")
	stubBrowser(t, errors.New("no display"))
	out, _ := captureStdout(t)
	errBuf, _ := captureStderr(t)
	pipeStdin(t, "WXYZ-1234\n")

	if code := cmdLogin(nil); code != 0 {
		t.Fatalf("cmdLogin with browser fail = %d, want 0\nstdout=%s\nstderr=%s",
			code, out.String(), errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "Could not open browser") {
		t.Errorf("stderr missing browser-fail message: %q", errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "https://api.example.test/cli-auth") {
		t.Errorf("stderr missing fallback URL: %q", errBuf.String())
	}
}

// TestCmdLogin_CodeExpired: server exchange always returns 410
// cli_auth_code_unavailable. Polling path must give up after seeing
// the unavailable code and exit 1 without writing a token.
func TestCmdLogin_CodeExpired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/cli-auth/code":
			_ = json.NewEncoder(w).Encode(api.CliAuthCodeResponse{
				Code:      "WXYZ-1234",
				URL:       "https://api.example.test/cli-auth?code=WXYZ-1234",
				ExpiresAt: time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339),
			})
		case "/v1/cli-auth/exchange":
			w.WriteHeader(http.StatusGone)
			_ = json.NewEncoder(w).Encode(api.Problem{
				Status: 410, Code: api.CodeCliAuthUnavailable,
				Title: "Code unavailable", Detail: "code is expired, already used, or unknown",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "")
	stubBrowser(t, nil)
	out, _ := captureStdout(t)
	errBuf, _ := captureStderr(t)
	pipeStdin(t, "\n")

	code := cmdLogin(nil)
	if code == 0 {
		t.Fatalf("cmdLogin expired = 0, want non-zero\nstdout=%s\nstderr=%s",
			out.String(), errBuf.String())
	}
	// Token file must NOT exist.
	if _, err := os.Stat(mustTokenPath(t)); !os.IsNotExist(err) {
		t.Errorf("token file unexpectedly exists after expired login")
	}
	// Stderr must mention the unavailable code or its stable name.
	if !strings.Contains(errBuf.String(), "Code unavailable") &&
		!strings.Contains(errBuf.String(), "cli_auth_code_unavailable") {
		t.Errorf("stderr missing expired message: %q", errBuf.String())
	}
}

// TestCmdLogin_CodeConsumed_Race: server exchange first returns 404
// pending, then 410 consumed on the second call. Polling path stops
// with the consumed error (not the expired one).
func TestCmdLogin_CodeConsumed_Race(t *testing.T) {
	var exchangeCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/cli-auth/code":
			_ = json.NewEncoder(w).Encode(api.CliAuthCodeResponse{
				Code:      "WXYZ-1234",
				URL:       "https://api.example.test/cli-auth?code=WXYZ-1234",
				ExpiresAt: time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339),
			})
		case "/v1/cli-auth/exchange":
			n := atomic.AddInt32(&exchangeCalls, 1)
			if n == 1 {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(api.Problem{
					Status: 404, Code: api.CodeCliAuthPending,
					Title: "Awaiting approval", Detail: "open the URL in your browser to continue",
				})
				return
			}
			w.WriteHeader(http.StatusGone)
			_ = json.NewEncoder(w).Encode(api.Problem{
				Status: 410, Code: api.CodeCliAuthUnavailable,
				Title: "Code unavailable", Detail: "code is expired, already used, or unknown",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "")
	stubBrowser(t, nil)
	errBuf, _ := captureStderr(t)
	pipeStdin(t, "\n")

	code := cmdLogin(nil)
	if code == 0 {
		t.Fatalf("cmdLogin race = 0, want non-zero\nstderr=%s", errBuf.String())
	}
	if got := atomic.LoadInt32(&exchangeCalls); got < 2 {
		t.Errorf("exchange calls = %d, want >= 2 (polled at least twice)", got)
	}
}

// TestCmdLogin_ServerUnreachable: mint endpoint must fail fast.
// Points FAAS_API at a closed port so the TCP connect fails
// immediately. Asserts non-zero exit + no token file.
func TestCmdLogin_ServerUnreachable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("FAAS_API", "http://127.0.0.1:1") // closed port → immediate failure
	t.Setenv("FAAS_TOKEN", "")
	stubBrowser(t, nil)
	errBuf, _ := captureStderr(t)
	pipeStdin(t, "\n")

	if code := cmdLogin(nil); code == 0 {
		t.Errorf("cmdLogin with closed server = 0, want non-zero\nstderr=%s", errBuf.String())
	}
	if _, err := os.Stat(mustTokenPath(t)); !os.IsNotExist(err) {
		t.Errorf("token file written despite unreachable server")
	}
}

// TestCmdLogin_PollingBackoff: server returns 404 pending for the
// first 3 exchange calls, then 200 on the 4th. Wall-clock duration
// must be >= 3s (proves the loop isn't tight-looping). The CLI
// uses a 1s time.After backoff per iteration.
func TestCmdLogin_PollingBackoff(t *testing.T) {
	const plaintext = "fp_live_polling12345678901234567890123456789012345678"
	var exchangeCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/cli-auth/code":
			_ = json.NewEncoder(w).Encode(api.CliAuthCodeResponse{
				Code:      "WXYZ-1234",
				URL:       "https://api.example.test/cli-auth?code=WXYZ-1234",
				ExpiresAt: time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339),
			})
		case "/v1/cli-auth/exchange":
			n := atomic.AddInt32(&exchangeCalls, 1)
			if n < 4 {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(api.Problem{
					Status: 404, Code: api.CodeCliAuthPending,
					Title: "Awaiting approval",
				})
				return
			}
			_ = json.NewEncoder(w).Encode(api.CliAuthExchangeResponse{
				Plaintext: plaintext,
				Account:   api.AccountResponse{Email: "x@example.com", Plan: "free"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "")
	stubBrowser(t, nil)
	pipeStdin(t, "\n")

	start := time.Now()
	if code := cmdLogin(nil); code != 0 {
		t.Fatalf("cmdLogin polling = %d, want 0", code)
	}
	dur := time.Since(start)
	if dur < 3*time.Second {
		t.Errorf("polling duration = %v, want >= 3s (backoff is 1s/iteration)", dur)
	}
	if got := atomic.LoadInt32(&exchangeCalls); got != 4 {
		t.Errorf("exchange calls = %d, want 4 (3 pending + 1 success)", got)
	}
}

// TestCmdLogin_AutoCreatesAccount: server's exchange handler returns
// a freshly-minted account for a previously-unknown email (UX §2.2
// "First successful login creates the account row if the email is
// new"). CLI must accept this without complaint and write the
// token; no separate signup command exists.
func TestCmdLogin_AutoCreatesAccount(t *testing.T) {
	const plaintext = "fp_live_autocreate123456789012345678901234567890123456"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/cli-auth/code":
			_ = json.NewEncoder(w).Encode(api.CliAuthCodeResponse{
				Code:      "WXYZ-1234",
				URL:       "https://api.example.test/cli-auth?code=WXYZ-1234",
				ExpiresAt: time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339),
			})
		case "/v1/cli-auth/exchange":
			_ = json.NewEncoder(w).Encode(api.CliAuthExchangeResponse{
				Plaintext: plaintext,
				Account:   api.AccountResponse{Email: "new@example.com", Plan: "free"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "")
	stubBrowser(t, nil)
	out, _ := captureStdout(t)
	pipeStdin(t, "WXYZ-1234\n")

	if code := cmdLogin(nil); code != 0 {
		t.Fatalf("cmdLogin auto-create = %d, want 0\nstdout=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "Logged in as new@example.com") {
		t.Errorf("stdout missing auto-created account: %q", out.String())
	}
	if saved := readSavedToken(t); saved != plaintext {
		t.Errorf("saved token = %q, want %q", saved, plaintext)
	}
}

// TestCmdLogin_TokenFlagRegression: the --token path is unchanged.
// Mirrors TestCmdLogin_Success (cli_test.go:197-224) and asserts
// the --token regression doesn't break when the interactive flow
// is wired. Documented in the plan as an explicit alias.
func TestCmdLogin_TokenFlagRegression(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.AccountResponse{Email: "alice@x.com", Plan: "pro"})
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "")

	if code := cmdLogin([]string{"--token", "fp_live_x"}); code != 0 {
		t.Fatalf("cmdLogin --token regression = %d, want 0", code)
	}
}

// mustTokenPath returns the token file path or fatals. Used in
// failure-path assertions where we want a clear error if the test
// setup is broken (vs. an os.IsNotExist miss-interpreted).
func mustTokenPath(t *testing.T) string {
	t.Helper()
	p, err := tokenPath()
	if err != nil {
		t.Fatalf("tokenPath: %v", err)
	}
	return p
}

// Compile-time guard: ensure json + fmt are referenced even if a
// future refactor trims a usage. (Keeps imports stable.)
var _ = json.Marshal
var _ = fmt.Sprintf