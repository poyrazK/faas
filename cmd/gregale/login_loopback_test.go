package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/browser"
)

// clickingBrowser stands in for the user's browser in the one-click flow:
// "opening" the approval page approves it and follows the server's
// redirect to the CLI's loopback callback with the given query.
type clickingBrowser struct {
	t        *testing.T
	callback func(page *url.URL) url.Values
	status   chan int
}

func (b *clickingBrowser) Launch(target string) error {
	page, err := url.Parse(target)
	if err != nil {
		return err
	}
	q := b.callback(page)
	go func() {
		resp, err := http.Get("http://127.0.0.1:" + page.Query().Get("port") + api.CliAuthLoopbackCallbackPath + "?" + q.Encode())
		if err != nil {
			b.t.Errorf("callback: %v", err)
			b.status <- 0
			return
		}
		_ = resp.Body.Close()
		b.status <- resp.StatusCode
	}()
	return nil
}

func oneClickEnv(t *testing.T, apiURL string, b browser.Launcher) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("FAAS_API", apiURL)
	t.Setenv("FAAS_TOKEN", "")
	for _, k := range []string{"SSH_CONNECTION", "SSH_TTY", "SSH_CLIENT"} {
		t.Setenv(k, "")
	}
	old := browser.Default
	browser.Default = b
	t.Cleanup(func() { browser.Default = old })
	setFakeKeyring(t)
}

// oneClickServer mints a loopback code and exchanges only the device code
// the "browser" delivers.
func oneClickServer(t *testing.T, plaintext, delivered string, exchanged *int32) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/cli-auth/code":
			var req api.CliAuthCodeRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if !req.Device || req.LoopbackPort == 0 {
				t.Errorf("mint request = %+v, want device + loopback port", req)
			}
			_ = json.NewEncoder(w).Encode(api.CliAuthCodeResponse{
				Code:       "ABCD-1234",
				URL:        "https://api.example.test/cli-auth?code=ABCD-1234&port=" + strconv.Itoa(req.LoopbackPort),
				ExpiresAt:  time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339),
				DeviceCode: strings.Repeat("11", 32),
				Loopback:   true,
			})
		case "/v1/cli-auth/exchange":
			var req api.CliAuthExchangeRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			atomic.AddInt32(exchanged, 1)
			if req.DeviceCode != delivered {
				w.WriteHeader(http.StatusGone)
				_ = json.NewEncoder(w).Encode(api.Problem{Status: 410, Code: api.CodeCliAuthUnavailable, Title: "Code unavailable"})
				return
			}
			_ = json.NewEncoder(w).Encode(api.CliAuthExchangeResponse{
				Plaintext: plaintext,
				Account:   api.AccountResponse{Email: "one-click@example.com", Plan: "free"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// `gregale login` asks for no code anywhere: the browser click delivers the
// device code to the CLI's loopback listener (ADR-20261111170000).
func TestCmdLogin_OneClickLoopback(t *testing.T) {
	plaintext := testAPIKey('l')
	delivered := strings.Repeat("22", 32)
	var exchanged int32
	srv := oneClickServer(t, plaintext, delivered, &exchanged)
	b := &clickingBrowser{t: t, status: make(chan int, 1), callback: func(page *url.URL) url.Values {
		return url.Values{"code": {"ABCD1234"}, "device_code": {delivered}}
	}}
	oneClickEnv(t, srv.URL, b)
	out, _ := captureStdout(t)

	if code := cmdLogin(nil); code != 0 {
		t.Fatalf("cmdLogin = %d\nstdout=%s", code, out.String())
	}
	if status := <-b.status; status != http.StatusOK {
		t.Fatalf("callback status = %d, want 200", status)
	}
	if saved := readSavedToken(t); saved != plaintext {
		t.Fatalf("saved token = %q, want %q", saved, plaintext)
	}
	if strings.Contains(out.String(), "ABCD-1234") || strings.Contains(strings.ToLower(out.String()), "your code") {
		t.Errorf("one-click login shows a code to type: %q", out.String())
	}
	if !strings.Contains(out.String(), "Click Authorize") || !strings.Contains(out.String(), "Logged in as one-click@example.com") {
		t.Errorf("stdout = %q", out.String())
	}
	if got := atomic.LoadInt32(&exchanged); got != 1 {
		t.Errorf("exchanges = %d, want 1", got)
	}
}

// A callback for another login's code (a page probing the port) is refused
// without an exchange; the real one still completes.
func TestCmdLogin_OneClickRejectsForeignCallback(t *testing.T) {
	plaintext := testAPIKey('m')
	delivered := strings.Repeat("33", 32)
	var exchanged int32
	srv := oneClickServer(t, plaintext, delivered, &exchanged)
	foreign := make(chan int, 1)
	b := &clickingBrowser{t: t, status: make(chan int, 1), callback: func(page *url.URL) url.Values {
		resp, err := http.Get("http://127.0.0.1:" + page.Query().Get("port") + api.CliAuthLoopbackCallbackPath + "?code=FFFF0000&device_code=" + strings.Repeat("44", 32))
		if err != nil {
			t.Errorf("foreign callback: %v", err)
			foreign <- 0
		} else {
			_ = resp.Body.Close()
			foreign <- resp.StatusCode
		}
		return url.Values{"code": {"ABCD-1234"}, "device_code": {delivered}}
	}}
	oneClickEnv(t, srv.URL, b)
	out, _ := captureStdout(t)

	if code := cmdLogin(nil); code != 0 {
		t.Fatalf("cmdLogin = %d\nstdout=%s", code, out.String())
	}
	if status := <-foreign; status != http.StatusBadRequest {
		t.Errorf("foreign callback status = %d, want 400", status)
	}
	<-b.status
	if got := atomic.LoadInt32(&exchanged); got != 1 {
		t.Errorf("exchanges = %d, want 1 (foreign callback must not exchange)", got)
	}
}

// --device keeps the typed code for a browser on another computer.
func TestCmdLogin_DeviceFlagUsesTypedCode(t *testing.T) {
	plaintext := testAPIKey('n')
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/cli-auth/code":
			var req api.CliAuthCodeRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.LoopbackPort != 0 {
				t.Errorf("--device minted a loopback code: %+v", req)
			}
			_ = json.NewEncoder(w).Encode(api.CliAuthCodeResponse{
				Code:      "ABCD-1234",
				URL:       "https://api.example.test/cli-auth?code=ABCD-1234",
				ExpiresAt: time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339),
			})
		case "/v1/cli-auth/exchange":
			_ = json.NewEncoder(w).Encode(api.CliAuthExchangeResponse{
				Plaintext: plaintext,
				Account:   api.AccountResponse{Email: "device@example.com", Plan: "free"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	oneClickEnv(t, srv.URL, &stubLauncher{})
	fastLoginPoll(t)
	out, _ := captureStdout(t)

	if code := cmdLogin([]string{"--device"}); code != 0 {
		t.Fatalf("cmdLogin --device = %d\nstdout=%s", code, out.String())
	}
	if !strings.Contains(out.String(), "Your code: ABCD-1234") {
		t.Errorf("stdout missing the code to type: %q", out.String())
	}
}
