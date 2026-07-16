package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// --- seedDevAccount --------------------------------------------------------

func TestSeedDevAccount_ValidToken(t *testing.T) {
	s := state.NewMemStore()
	// APIKeyPrefix + 48 hex chars (24 random bytes, hex-encoded).
	tok := api.APIKeyPrefix + "abcdef1234567890abcdef1234567890abcdef1234567890"
	if err := seedDevAccount(context.Background(), s, tok); err != nil {
		t.Fatalf("seedDevAccount: %v", err)
	}
	acct, err := s.AccountByKeyHash(context.Background(), api.HashAPIKey(tok))
	if err != nil {
		t.Fatalf("AccountByKeyHash: %v", err)
	}
	if acct.Email != "dev@local" {
		t.Errorf("email = %q, want dev@local", acct.Email)
	}
	if acct.Plan != api.PlanFree {
		t.Errorf("plan = %v, want free", acct.Plan)
	}
}

func TestSeedDevAccount_InvalidToken(t *testing.T) {
	s := state.NewMemStore()
	err := seedDevAccount(context.Background(), s, "not-a-valid-key")
	if err == nil {
		t.Fatal("expected error for invalid API key format")
	}
	if got := err.Error(); !contains(got, "FAAS_DEV_TOKEN") {
		t.Errorf("error %q missing FAAS_DEV_TOKEN context", got)
	}
}

// --- runWithDeps -----------------------------------------------------------

func TestRunWithDeps_ListenErrorReturns(t *testing.T) {
	deps := defaultDeps()
	deps.listen = func(_, _ string) (net.Listener, error) {
		return nil, errors.New("addr in use")
	}
	err := runWithDeps(context.Background(), discardLogger(), deps)
	if err == nil {
		t.Fatal("expected listen error")
	}
	if !contains(err.Error(), "addr in use") {
		t.Errorf("error %q missing 'addr in use'", err.Error())
	}
}

func TestRunWithDeps_ServesUntilCancel(t *testing.T) {
	deps := defaultDeps()
	// Let runWithDeps own the listener (more realistic).
	var capturedAddr atomic.Value
	deps.listen = func(_, _ string) (net.Listener, error) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		capturedAddr.Store(l.Addr().String())
		return l, nil
	}

	// Seed env so FAAS_DEV_TOKEN path runs — proves seedDevAccount integration.
	tok := api.APIKeyPrefix + "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	deps.getenv = func(k string) string {
		if k == "FAAS_DEV_TOKEN" {
			return tok
		}
		return ""
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runWithDeps(ctx, discardLogger(), deps) }()
	t.Cleanup(cancel)

	// Wait for the listen address to be captured, then for Accept to be ready.
	deadline := time.Now().Add(3 * time.Second)
	var addr string
	for time.Now().Before(deadline) {
		if v := capturedAddr.Load(); v != nil {
			addr = v.(string)
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if addr == "" {
		// Surface the goroutine error so we know why runWithDeps returned.
		cancel()
		select {
		case err := <-done:
			t.Fatalf("runWithDeps returned %v before binding listener", err)
		case <-time.After(time.Second):
			t.Fatal("listener address never captured and runWithDeps didn't return")
		}
	}
	// Bounded wait for Accept — httpSrv.Serve is in a goroutine.
	for time.Now().Before(deadline) {
		c, derr := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if derr == nil {
			_ = c.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Issue the GET.
	cli := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest("GET", "http://"+addr+"/v1/account", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := cli.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/account: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200 (dev token should authenticate)", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil && !contains(err.Error(), "Server closed") && !contains(err.Error(), "use of closed network connection") {
			t.Errorf("runWithDeps returned %v, want clean shutdown", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runWithDeps did not return after ctx cancel")
	}
}

func TestRunWithDeps_SeedFailureReturns(t *testing.T) {
	deps := defaultDeps()
	deps.getenv = func(k string) string {
		if k == "FAAS_DEV_TOKEN" {
			return "garbage"
		}
		return ""
	}
	// No listen needed — we error before reaching the listener.
	err := runWithDeps(context.Background(), discardLogger(), deps)
	if err == nil {
		t.Fatal("expected seedDevAccount error")
	}
}

func TestRunWithDeps_ServeError(t *testing.T) {
	// Closed listener → Serve errors immediately.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = ln.Close()

	deps := defaultDeps()
	deps.listen = func(_, _ string) (net.Listener, error) { return ln, nil }

	done := make(chan error, 1)
	go func() { done <- runWithDeps(context.Background(), discardLogger(), deps) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runWithDeps did not exit after closed listener")
	}
}

func TestRunWithDeps_StoreCalledExactlyOnce(t *testing.T) {
	deps := defaultDeps()
	var calls atomic.Int32
	deps.store = func() state.Store {
		calls.Add(1)
		return state.NewMemStore()
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	deps.listen = func(_, _ string) (net.Listener, error) { return ln, nil }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runWithDeps(ctx, discardLogger(), deps) }()

	// Give it a moment to call deps.store().
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	if got := calls.Load(); got != 1 {
		t.Errorf("deps.store calls = %d, want 1", got)
	}
}

func TestDefaultDeps_ReturnExpected(t *testing.T) {
	d := defaultDeps()
	if d.listen == nil {
		t.Error("defaultDeps().listen is nil")
	}
	if d.store == nil {
		t.Error("defaultDeps().store is nil")
	}
	if d.getenv == nil {
		t.Error("defaultDeps().getenv is nil")
	}
	if d.newSrv == nil {
		t.Error("defaultDeps().newSrv is nil")
	}
	// Sanity: store() returns a usable Store.
	s := d.store()
	if s == nil {
		t.Error("defaultDeps().store() returned nil")
	}
	srv := d.newSrv(":0", http.NewServeMux())
	if srv.ReadHeaderTimeout == 0 {
		t.Error("default server should set ReadHeaderTimeout")
	}
}

// --- newServer + auth extra coverage --------------------------------------

func TestNewServer_DefaultDomain(t *testing.T) {
	s := newServer(state.NewMemStore(), discardLogger(), "", noopNotifier{})
	if s.domain != "DOMAIN" {
		t.Errorf("domain = %q, want DOMAIN fallback", s.domain)
	}
}

func TestNewServer_CustomDomain(t *testing.T) {
	s := newServer(state.NewMemStore(), discardLogger(), "apps.example.com", noopNotifier{})
	if s.domain != "apps.example.com" {
		t.Errorf("domain = %q", s.domain)
	}
}

func TestAuthActiveAccountAllowed(t *testing.T) {
	s := state.NewMemStore()
	tok := api.APIKeyPrefix + "0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := seedDevAccount(context.Background(), s, tok); err != nil {
		t.Fatal(err)
	}
	srv := newServer(s, discardLogger(), "", noopNotifier{})
	h := srv.handler()
	req := httptestRequest("GET", "/v1/account", tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("active account status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestAuth_RejectsInvalidFormat covers the format-validation branch: a header
// that doesn't match the API key shape must be rejected with 401 before any
// store lookup.
func TestAuth_RejectsInvalidFormat(t *testing.T) {
	s := state.NewMemStore()
	srv := newServer(s, discardLogger(), "", noopNotifier{})
	h := srv.handler()
	for _, bad := range []string{"not-a-key", "fp_live_short", "fp_live_toolong_zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"} {
		req := httptestRequest("GET", "/v1/account", bad)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("invalid token %q: status = %d, want 401", bad, rec.Code)
		}
	}
}

// TestAuth_RejectsUnknownKey covers the post-format, post-store branch: the
// token format is valid but no account holds it. Server must respond 401 and
// MUST NOT leak which side of the check failed (cf. spec §11).
func TestAuth_RejectsUnknownKey(t *testing.T) {
	s := state.NewMemStore()
	tok := api.APIKeyPrefix + "feedfacefeedfacefeedfacefeedfacefeedfacefeedface"
	srv := newServer(s, discardLogger(), "", noopNotifier{})
	h := srv.handler()
	req := httptestRequest("GET", "/v1/account", tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unknown key status = %d, want 401 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestBearerToken(t *testing.T) {
	cases := []struct {
		header, want string
	}{
		{"Bearer abc", "abc"},
		{"Bearer   abc  ", "abc"},
		{"Basic abc", ""},
		{"", ""},
		{"Bearer", ""},
	}
	for _, tc := range cases {
		req := httptestRequestWithAuthHeader("GET", "/", tc.header)
		if got := bearerToken(req); got != tc.want {
			t.Errorf("bearerToken(%q) = %q, want %q", tc.header, got, tc.want)
		}
	}
}

func TestDecodeJSON_RejectsUnknownFields(t *testing.T) {
	body := `{"slug":"ok","rogue":1}`
	req, _ := http.NewRequest("POST", "/", bytes.NewBufferString(body))
	var dst struct {
		Slug string `json:"slug"`
	}
	err := decodeJSON(req, &dst)
	if err == nil {
		t.Fatal("expected error on unknown JSON field")
	}
}

// --- helpers ---------------------------------------------------------------

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func httptestRequest(method, path, tok string) *http.Request {
	return httptestRequestWithAuthHeader(method, path, "Bearer "+tok)
}

func httptestRequestWithAuthHeader(method, path, auth string) *http.Request {
	req, _ := http.NewRequest(method, path, nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	return req
}
