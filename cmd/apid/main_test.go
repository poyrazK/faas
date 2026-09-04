package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/state"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newLocalListener is the test seam for httptest.Server when the test
// wants to construct the http.Server directly (httptest.NewUnstartedServer
// takes an http.Handler, not an *http.Server). Issue #995 Phase 1 helper.
func newLocalListener(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return l
}

// withTestHMACFiles overrides FAAS_AUDIT_HMAC_KEY_FILE,
// FAAS_RECOVERY_HMAC_KEY_FILE, and (ADR-117 PR-C)
// FAAS_HOST_HMAC_KEY_PATH for the lifetime of the test so
// loadOrGenerate{Audit,Recovery}HMACKey + loadHostHMACKey
// auto-mint a fresh key in t.TempDir() rather than refusing to
// start (production-grade strict-mode for the recovery-hmac.key
// + host.hmac.key paths; the audit-hmac.key path tolerates a nil
// but the recovery + env-diff paths do not).
//
// The env-var overrides are unset by t.Cleanup. The tmp keys
// persist for the test's lifetime — the loaders pass them
// through to their respective SetHMACSecret + hostHMACKey
// seams, so the audit + recovery + env-diff HMAC keys are live
// for the duration of the test process.
//
// Use this from any test that calls runWithDeps directly, since
// the boot-time loaders call os.Stat on /var/lib/faas (audit +
// recovery) and /etc/faas/secrets (env-diff) and refuse to
// start if neither the env var nor the file yields a key.
func withTestHMACFiles(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	auditKey := make([]byte, 32)
	for i := range auditKey {
		auditKey[i] = 0xAB
	}
	recoveryKey := make([]byte, 32)
	for i := range recoveryKey {
		recoveryKey[i] = 0xCD
	}
	envDiffKey := make([]byte, 32)
	for i := range envDiffKey {
		envDiffKey[i] = 0xEF
	}
	if err := os.WriteFile(filepath.Join(dir, "audit-hmac.key"), []byte(hex.EncodeToString(auditKey)+"\n"), 0o600); err != nil {
		t.Fatalf("write audit-hmac.key: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "recovery-hmac.key"), []byte(hex.EncodeToString(recoveryKey)+"\n"), 0o600); err != nil {
		t.Fatalf("write recovery-hmac.key: %v", err)
	}
	// host.hmac.key is 32 RAW bytes (NOT hex-encoded) per the
	// loadHostHMACKey loader; the audit + recovery paths use
	// hex-encoded keys for filesystem portability, but the
	// env-diff key shares the raw-bytes posture with host.age.pub.
	if err := os.WriteFile(filepath.Join(dir, "host.hmac.key"), envDiffKey, 0o400); err != nil {
		t.Fatalf("write host.hmac.key: %v", err)
	}
	t.Setenv("FAAS_AUDIT_HMAC_KEY_FILE", filepath.Join(dir, "audit-hmac.key"))
	t.Setenv("FAAS_RECOVERY_HMAC_KEY_FILE", filepath.Join(dir, "recovery-hmac.key"))
	t.Setenv("FAAS_HOST_HMAC_KEY_PATH", filepath.Join(dir, "host.hmac.key"))
	// ADR-096 / PR-B kill-switch OFF for in-process apid tests. The
	// default-on gate (cmd/apid/main.go runAppErrorsServer boot path)
	// probes the `faas-apid` unix user via wire.ListenOrRecreateByName;
	// CI runners and dev Macs don't have that user, so the user lookup
	// blocks and runWithDeps never reaches httpSrv.Serve — which makes
	// TestRunWithDeps_ServesUntilCancel's GET /v1/account hit
	// "connection refused" inside its 3s deadline. Mirrors the
	// FAAS_APP_ERRORS_ENABLED=false env used in the e2e harness.
	t.Setenv("FAAS_APP_ERRORS_ENABLED", "false")
}

// --- seedDevAccount --------------------------------------------------------

func TestSeedDevAccount_ValidToken(t *testing.T) {
	s := state.NewMemStore()
	// APIKeyPrefix + 48 hex chars (24 random bytes, hex-encoded).
	tok := api.APIKeyPrefix + "abcdef1234567890abcdef1234567890abcdef1234567890"
	if err := seedDevAccount(context.Background(), s, tok); err != nil {
		t.Fatalf("seedDevAccount: %v", err)
	}
	// Tier A7 PR-D: seedDevAccount no longer mints the API key —
	// it only find-or-creates the dev@local account. Tests that
	// want the token to authenticate must mint the key themselves
	// (mirrors what pkg/e2etest.Harness.SeedAccount does for the
	// e2e harness).
	acct, err := s.AccountByEmail(context.Background(), "dev@local")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	if _, err := s.CreateAPIKey(context.Background(), acct.ID, api.HashAPIKey(tok), "dev", api.ScopesAdminOnly); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	got, err := s.AccountByKeyHash(context.Background(), api.HashAPIKey(tok))
	if err != nil {
		t.Fatalf("AccountByKeyHash: %v", err)
	}
	if got.Email != "dev@local" {
		t.Errorf("email = %q, want dev@local", got.Email)
	}
	if got.Plan != api.PlanFree {
		t.Errorf("plan = %v, want free", got.Plan)
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

func TestRunAppErrorsServer_RejectsPlaintextRemoteTarget(t *testing.T) {
	_, _, err := runAppErrorsServer(
		context.Background(),
		"tcp://127.0.0.1:9093",
		nil,
		nil,
		nil,
		discardLogger(),
	)
	if err == nil {
		t.Fatal("remote AppErrors target without TLS should be rejected")
	}
	if !contains(err.Error(), "mTLS is required") {
		t.Fatalf("error = %q, want mTLS requirement", err)
	}
}

func TestResolvePrometheusURL(t *testing.T) {
	if got := resolvePrometheusURL(func(string) string { return "" }, role.RoleControlPlane); got != prometheusURLDefault {
		t.Fatalf("control-plane default = %q, want %q", got, prometheusURLDefault)
	}
	if got := resolvePrometheusURL(func(string) string { return "" }, role.RoleSingleBox); got != "" {
		t.Fatalf("single-box default = %q, want empty", got)
	}
	if got := resolvePrometheusURL(func(string) string { return "http://prometheus.example:9095" }, role.RoleControlPlane); got != "http://prometheus.example:9095" {
		t.Fatalf("explicit URL = %q", got)
	}
}

// --- runWithDeps -----------------------------------------------------------

// withBillingKeysForTest explicitly selects the legacy Paddle adapter and
// seeds the FAAS_PADDLE_* keys that pkg/billing/paddle.NewProvider requires
// at construction time (PR #962 CRIT-2 fix; pre-fix the SDK accepted empty
// keys silently and the loader warn-logged per-tick instead of refusing to
// boot).
//
// runWithDeps exercises the full apid boot path including the
// billing-loader step at main.go:1078. A test that exercises the
// boot path without these seeds fails with the new empty-key guard
// before reaching whatever behaviour the test was actually pinning
// (listen error, verifier wiring, cancel signal). Use this from
// every runWithDeps call site that doesn't care about billing
// itself — the keys are sandbox-shaped so no real Paddle call
// can succeed, which is what these tests want.
func withBillingKeysForTest(t *testing.T) {
	t.Helper()
	// These lifecycle tests exercise listener/verifier wiring, not the
	// public-release Polar catalog preflight. Keep that dependency explicit so
	// a production default change cannot make the tests reach the network.
	t.Setenv("FAAS_BILLING_PROVIDER", "paddle")
	t.Setenv("FAAS_PADDLE_SANDBOX", "1")
	t.Setenv("FAAS_PADDLE_API_KEY", "pdl_test_load_runwithdeps")
}

// withTestMailTransport collapses FAAS_MAIL_TRANSPORT to "log" so
// the runWithDeps boot path passes the new issue #246 fail-closed
// contract (cmd/apid/main.go:977) without exercising the mail
// factory itself. The dedicated factory contract is pinned by
// TestMailFactory_PicksCorrectTransport in mail_wiring_test.go;
// runWithDeps lifecycle tests (listen error, verifier wiring,
// cancel signal) only care that the boot reaches their real
// assertion surface, and an unset transport would now abort before
// that surface is reached. The dedicated fail-closed rows in
// TestMailFactory_PicksCorrectTransport cover the abort path.
func withTestMailTransport(t *testing.T) {
	t.Helper()
	t.Setenv("FAAS_MAIL_TRANSPORT", "log")
	t.Setenv("FAAS_MAIL_FROM", "test@gregale.test")
}

func TestRunWithDeps_ListenErrorReturns(t *testing.T) {
	withTestHMACFiles(t)
	withBillingKeysForTest(t)
	withTestMailTransport(t)
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
	withTestHMACFiles(t)
	withBillingKeysForTest(t)
	withTestMailTransport(t)
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
		// Fall through to the real env for the HMAC key files so
		// withTestHMACFiles picks up the t.Setenv overrides; the
		// closure above used to swallow them, which broke once
		// the recovery-hmac loader (commit 7) started refusing to
		// start without a real key (audit-hmac loader tolerates nil
		// but the recovery loader does not).
		return os.Getenv(k)
	}
	// Tier A7 PR-D: seedDevAccount no longer mints the key. Seed
	// one explicitly so the auth path has something to resolve.
	preSeed := state.NewMemStore()
	if err := seedDevAccount(context.Background(), preSeed, tok); err != nil {
		t.Fatalf("seedDevAccount: %v", err)
	}
	preAcct, err := preSeed.AccountByEmail(context.Background(), "dev@local")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	if _, err := preSeed.CreateAPIKey(context.Background(), preAcct.ID, api.HashAPIKey(tok), "dev", api.ScopesAdminOnly); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	deps.store = func() state.Store { return preSeed }

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
	withTestHMACFiles(t)
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
	withTestHMACFiles(t)
	withBillingKeysForTest(t)
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
	withTestHMACFiles(t)
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
	srv := d.newSrv(":0", http.NewServeMux(), nil)
	if srv.ReadHeaderTimeout == 0 {
		t.Error("default server should set ReadHeaderTimeout")
	}
	// Issue #995 Phase 1 / ADR-121: hardened defaults surface when
	// newSrv is called with a nil cfg (the legacy DI seam still
	// works for callers that don't have a config in hand).
	if srv.ReadTimeout == 0 {
		t.Error("default server should set ReadTimeout (issue #995 Phase 1)")
	}
	if srv.WriteTimeout == 0 {
		t.Error("default server should set WriteTimeout (issue #995 Phase 1)")
	}
	if srv.IdleTimeout == 0 {
		t.Error("default server should set IdleTimeout (issue #995 Phase 1)")
	}
	if srv.MaxHeaderBytes == 0 {
		t.Error("default server should set MaxHeaderBytes (issue #995 Phase 1)")
	}
}

// TestNewSrv_AppliesTimeouts verifies that newSrv, when called with a
// resolved *Config, applies the four hardened listener fields
// (ReadTimeout, WriteTimeout, IdleTimeout, MaxHeaderBytes). Issue
// #995 Phase 1 / ADR-121.
func TestNewSrv_AppliesTimeouts(t *testing.T) {
	d := defaultDeps()
	cfg := &Config{
		RequestReadTimeout:    7 * time.Second,
		RequestWriteTimeout:   8 * time.Second,
		RequestIdleTimeout:    9 * time.Second,
		RequestMaxHeaderBytes: 1234,
	}
	srv := d.newSrv(":0", http.NewServeMux(), cfg)
	if srv.ReadTimeout != 7*time.Second {
		t.Errorf("ReadTimeout = %v, want 7s", srv.ReadTimeout)
	}
	if srv.WriteTimeout != 8*time.Second {
		t.Errorf("WriteTimeout = %v, want 8s", srv.WriteTimeout)
	}
	if srv.IdleTimeout != 9*time.Second {
		t.Errorf("IdleTimeout = %v, want 9s", srv.IdleTimeout)
	}
	if srv.MaxHeaderBytes != 1234 {
		t.Errorf("MaxHeaderBytes = %d, want 1234", srv.MaxHeaderBytes)
	}
}

// TestConfig_GetRequestTimeouts_DefaultsAndEnv verifies the Get helpers
// honour env overlay → TOML → defaults. Issue #995 Phase 1 / ADR-121.
func TestConfig_GetRequestTimeouts_DefaultsAndEnv(t *testing.T) {
	// Defaults: empty Config + empty env → fall back to
	// api.APID*SecondsDefault.
	var c Config
	if got := c.GetRequestReadTimeout(func(string) string { return "" }); got != time.Duration(api.APIDReadTimeoutSecondsDefault)*time.Second {
		t.Errorf("ReadTimeout default = %v, want %ds", got, api.APIDReadTimeoutSecondsDefault)
	}
	if got := c.GetRequestWriteTimeout(func(string) string { return "" }); got != time.Duration(api.APIDWriteTimeoutSecondsDefault)*time.Second {
		t.Errorf("WriteTimeout default = %v, want %ds", got, api.APIDWriteTimeoutSecondsDefault)
	}
	if got := c.GetRequestIdleTimeout(func(string) string { return "" }); got != time.Duration(api.APIDIdleTimeoutSecondsDefault)*time.Second {
		t.Errorf("IdleTimeout default = %v, want %ds", got, api.APIDIdleTimeoutSecondsDefault)
	}
	if got := c.GetRequestMaxHeaderBytes(func(string) string { return "" }); got != api.DefaultMaxHeaderBytes {
		t.Errorf("MaxHeaderBytes default = %d, want %d", got, api.DefaultMaxHeaderBytes)
	}

	// TOML overrides defaults.
	c.RequestReadTimeout = 5 * time.Second
	if got := c.GetRequestReadTimeout(func(string) string { return "" }); got != 5*time.Second {
		t.Errorf("ReadTimeout TOML = %v, want 5s", got)
	}

	// Env overrides TOML.
	if got := c.GetRequestReadTimeout(func(string) string { return "3s" }); got != 3*time.Second {
		t.Errorf("ReadTimeout env = %v, want 3s", got)
	}
	if got := c.GetRequestMaxHeaderBytes(func(string) string { return "2048" }); got != 2048 {
		t.Errorf("MaxHeaderBytes env = %d, want 2048", got)
	}

	// Malformed env falls through to TOML.
	if got := c.GetRequestReadTimeout(func(string) string { return "not-a-duration" }); got != 5*time.Second {
		t.Errorf("ReadTimeout malformed env = %v, want 5s (TOML)", got)
	}
}

// TestParsePositiveInt verifies the small helper that backs
// GetRequestMaxHeaderBytes' env overlay. Issue #995 Phase 1 helper.
func TestParsePositiveInt(t *testing.T) {
	cases := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"0", 0, false},
		{"1", 1, false},
		{"1048576", 1048576, false},
		{"", 0, true},
		{"-1", 0, true},
		{"12x", 0, true},
		{"1.5", 0, true},
		// Review fix: the prior n<0 check caught only negative
		// wraps; positive wraps slipped through and produced a
		// garbage int64. PR-#996 review finding #4 (medium).
		{"10000000000000000000", 0, true},                   // 1e19 — wraps positive on int64
		{"9223372036854775808", 0, true},                    // MaxInt64 + 1 — overflows
		{"9223372036854775807", 9223372036854775807, false}, // MaxInt64 — admit
	}
	for _, tc := range cases {
		got, err := parsePositiveInt(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("parsePositiveInt(%q) err = %v, wantErr = %v", tc.in, err, tc.wantErr)
		}
		if !tc.wantErr && got != tc.want {
			t.Errorf("parsePositiveInt(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestNewSrv_OversizedHeaders_Returns431 verifies that an http.Server
// built with the hardened defaults rejects requests with headers
// larger than MaxHeaderBytes (431 Request Header Fields Too Large).
// Issue #995 Phase 1 / ADR-121 acceptance criterion #2.
//
// stdlib's http.Server returns 431 via http.MaxHeaderError when the
// header block during read exceeds MaxHeaderBytes; clients may also
// see a transport-level error (the server closes the conn). Either
// signal proves the cap fired — accept both.
func TestNewSrv_OversizedHeaders_Returns431(t *testing.T) {
	d := defaultDeps()
	cfg := &Config{
		RequestMaxHeaderBytes: 64, // small enough to trigger on a 4 KiB header
	}
	srv := d.newSrv(":0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), cfg)

	ts := &httptest.Server{
		Listener: newLocalListener(t),
		Config:   srv,
	}
	ts.Start()
	defer ts.Close()

	// A single header line with name "X-Big" + ": " + 4 KiB body.
	// 4 KiB exceeds 64-byte MaxHeaderBytes by orders of magnitude.
	big := bytes.Repeat([]byte("X"), 4096)
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/x", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("X-Big", string(big))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Client saw a transport error (server closed the conn mid-
		// read). That's still a hardening signal.
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestHeaderFieldsTooLarge {
		t.Errorf("oversized headers: got %d, want 431", resp.StatusCode)
	}
}

// TestNewSrv_SlowBody_ReturnsTimeout verifies that ReadTimeout
// actually fires on a client that dribbles bytes slower than the
// configured window. We use a tiny ReadTimeout (50ms) and a body
// writer that sleeps between bytes. Issue #995 Phase 1 / ADR-121
// acceptance criterion #1 (slowloris defence on body arrival).
func TestNewSrv_SlowBody_ReturnsTimeout(t *testing.T) {
	d := defaultDeps()
	cfg := &Config{
		RequestReadTimeout: 50 * time.Millisecond,
	}
	srv := d.newSrv(":0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drain whatever the client sent (or didn't). The handler
		// being reachable isn't the point — the server's
		// ReadTimeout is what we're exercising.
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}), cfg)

	ts := &httptest.Server{
		Listener: newLocalListener(t),
		Config:   srv,
	}
	ts.Start()
	defer ts.Close()

	pr, pw := io.Pipe()
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/x", pr)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.ContentLength = -1 // chunked; ReadTimeout is the only thing keeping this alive

	errCh := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			errCh <- err
			return
		}
		_ = resp.Body.Close()
		errCh <- nil
	}()

	// Dribble a single byte, then sleep well past ReadTimeout.
	if _, err := pw.Write([]byte("a")); err != nil {
		t.Fatalf("pw.Write: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	// Close the pipe so the goroutine doesn't leak if the test
	// races; the server will already have aborted the read.
	_ = pw.Close()

	select {
	case <-errCh:
		// Client saw a transport error (server closed the conn)
		// — that's the hardening path firing.
	case <-time.After(2 * time.Second):
		t.Error("ReadTimeout did not fire within 2s — slowloris defence missing")
	}
}

// --- newServer + auth extra coverage --------------------------------------

func TestNewServer_DefaultDomain(t *testing.T) {
	s := newServer(state.NewMemStore(), discardLogger(), "", noopNotifier{})
	if s.domain != "unset" {
		t.Errorf("domain = %q, want unset sentinel fallback", s.domain)
	}
}

func TestNewServer_CustomDomain(t *testing.T) {
	s := newServer(state.NewMemStore(), discardLogger(), "apps.gregale.dev", noopNotifier{})
	if s.domain != "apps.gregale.dev" {
		t.Errorf("domain = %q", s.domain)
	}
}

func TestAuthActiveAccountAllowed(t *testing.T) {
	s := state.NewMemStore()
	tok := api.APIKeyPrefix + "0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := seedDevAccount(context.Background(), s, tok); err != nil {
		t.Fatal(err)
	}
	// Tier A7 PR-D: seedDevAccount no longer mints the key; mint
	// one explicitly so the auth path has something to resolve.
	acct, err := s.AccountByEmail(context.Background(), "dev@local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateAPIKey(context.Background(), acct.ID, api.HashAPIKey(tok), "dev", api.ScopesAdminOnly); err != nil {
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
