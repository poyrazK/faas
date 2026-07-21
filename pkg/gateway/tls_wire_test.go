// CertMagic wiring tests for gatewayd (spec §4.1, §11). These tests pin the
// DNS-01 / on-demand HTTP-01 wiring against a stubbed Hetzner DNS API so a
// production build of pkg/gateway/tls_wire.go is exercised end-to-end
// without hitting the live service. The cert-mint abuse-vector test
// (TestCertMagicOnDemand_AbuseVectorDenied) is the spec §11 closure test —
// an attacker who reaches :443 with an unowned SNI must not be served a
// valid cert.

package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/caddyserver/certmagic"
)

// testStorageDir returns a per-test storage dir under t.TempDir(). The dir
// does not exist on return; NewCertMagicConfig (via ensureStorageDir) is
// expected to create it.
func testStorageDir(t *testing.T) string {
	t.Helper()
	return t.TempDir() + "/certs"
}

// newTestHetznerFactory wires a DNSProviderFactory against an httptest stub.
// Returns a closure suitable for NewCertMagicConfig's dnsFactory parameter.
func newTestHetznerFactory(_ *testing.T, h *fakeHetzner) DNSProviderFactory {
	return func(token, zone string) certmagic.DNSProvider {
		return newHetznerDNSProviderForTest(token, zone, h.server.URL+"/api/v1", h.server.Client())
	}
}

// validTLSConfig returns a TLSConfig that passes Validate() and produces a
// working CertMagic bundle when paired with a stubbed Hetzner. Tests must
// set StorageDir to a per-test value.
func validTLSConfig() TLSConfig {
	return TLSConfig{
		Disabled:                false,
		WildcardCertDomain:      "apps.example.com",
		HetznerDNSAPITokenPath:  "/dev/null", // not read by the factory path
		HetznerZone:             "example.com",
		StorageDir:              "", // caller fills per test
		OnDemandHTTP01Allowlist: StaticAllowlist(),
	}
}

// TestNewCertMagicConfig_BuildsBundle — happy path. Asserts every field of
// TLSBundle is populated.
func TestNewCertMagicConfig_BuildsBundle(t *testing.T) {
	h := newFakeHetzner(t)
	cfg := validTLSConfig()
	cfg.StorageDir = testStorageDir(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	bundle, err := NewCertMagicConfig(ctx, cfg, "test-token", quietLogger(), newTestHetznerFactory(t, h))
	if err != nil {
		t.Fatalf("NewCertMagicConfig: %v", err)
	}
	if bundle == nil {
		t.Fatal("bundle is nil")
	}
	if bundle.Config == nil {
		t.Error("bundle.Config is nil")
	}
	if bundle.GetCertificate == nil {
		t.Error("bundle.GetCertificate is nil (cmd/gatewayd/main.go needs this on tls.Config)")
	}
	if bundle.HTTPChallengeHandler == nil {
		t.Error("bundle.HTTPChallengeHandler is nil (needed for :80 ACME mux)")
	}
	if bundle.DecisionFunc == nil {
		t.Error("bundle.DecisionFunc is nil")
	}
}

// TestNewCertMagicConfig_ManageSyncReturnsCleanly — when the Hetzner stub
// returns 503 on POST /records (the path DNS-01 writes _acme-challenge TXT
// records through), NewCertMagicConfig must not propagate a startup error.
// A transient Hetzner outage shouldn't block the daemon — the wildcard
// cert will be obtained lazily on the first inbound request.
//
// What this actually exercises: certmagic v0.25's ManageSync short-circuits
// when an OnDemand config is present (config.go:380 — the domain is added
// to hostAllowlist and the obtain step is deferred). So our stub's 503
// never reaches the DNS provider during ManageSync. The "tolerate failure"
// log path in tls_wire.go:200-203 is dead code in v0.25 — the call returns
// nil even when the CA / DNS chain would fail. This test pins that
// contract: the constructor returns a bundle with no error. If a future
// certmagic upgrade changes the short-circuit (e.g. by always calling
// obtain), this test will catch the new failure mode via the error return.
func TestNewCertMagicConfig_ManageSyncReturnsCleanly(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/zones", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"zones":[{"id":"zone-abc","name":"example.com"}]}`))
	})
	mux.HandleFunc("/api/v1/records", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "simulated outage", http.StatusServiceUnavailable)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	factory := func(_, _ string) certmagic.DNSProvider {
		return newHetznerDNSProviderForTest("test", "example.com", srv.URL+"/api/v1", srv.Client())
	}

	cfg := validTLSConfig()
	cfg.StorageDir = testStorageDir(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	bundle, err := NewCertMagicConfig(ctx, cfg, "test-token", quietLogger(), factory)
	if err != nil {
		t.Fatalf("NewCertMagicConfig must tolerate ManageSync failure: %v", err)
	}
	if bundle == nil {
		t.Fatal("bundle is nil despite tolerated ManageSync failure")
	}
	// Belt-and-braces: a future certmagic upgrade that re-enables eager
	// obtain will surface here — the 503 propagates through ManageSync
	// and the assertion above fails. The contract pinned by this test is
	// "NewCertMagicConfig returns a non-nil bundle with no error when the
	// DNS provider stub is unreachable during ManageSync".
}

// TestNewCertMagicConfig_StagingCAWhenConfigured — UseStagingCA=true flips
// the issuer to Let's Encrypt staging.
func TestNewCertMagicConfig_StagingCAWhenConfigured(t *testing.T) {
	h := newFakeHetzner(t)
	cfg := validTLSConfig()
	cfg.StorageDir = testStorageDir(t)
	cfg.UseStagingCA = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	bundle, err := NewCertMagicConfig(ctx, cfg, "test-token", quietLogger(), newTestHetznerFactory(t, h))
	if err != nil {
		t.Fatalf("NewCertMagicConfig: %v", err)
	}
	if got := firstIssuerCA(bundle.Config); got != certmagic.LetsEncryptStagingCA {
		t.Errorf("UseStagingCA=true → issuer CA = %q, want %q", got, certmagic.LetsEncryptStagingCA)
	}
}

// TestNewCertMagicConfig_ProdCAWhenStagingDisabled — UseStagingCA=false (the
// production default) leaves the issuer CA as the Let's Encrypt prod URL.
func TestNewCertMagicConfig_ProdCAWhenStagingDisabled(t *testing.T) {
	h := newFakeHetzner(t)
	cfg := validTLSConfig()
	cfg.StorageDir = testStorageDir(t)
	cfg.UseStagingCA = false
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	bundle, err := NewCertMagicConfig(ctx, cfg, "test-token", quietLogger(), newTestHetznerFactory(t, h))
	if err != nil {
		t.Fatalf("NewCertMagicConfig: %v", err)
	}
	got := firstIssuerCA(bundle.Config)
	if got != certmagic.LetsEncryptProductionCA {
		t.Errorf("UseStagingCA=false → issuer CA = %q, want %q", got, certmagic.LetsEncryptProductionCA)
	}
}

// firstIssuerCA returns the CA URL of the first ACMEIssuer in the bundle.
// Returns "" if the bundle has no ACMEIssuer.
func firstIssuerCA(cfg *certmagic.Config) string {
	if cfg == nil || len(cfg.Issuers) == 0 {
		return ""
	}
	if am, ok := cfg.Issuers[0].(*certmagic.ACMEIssuer); ok {
		return am.CA
	}
	return ""
}

// TestNewCertMagicConfig_DisabledConfigRejected — Disabled=true is the
// e2e-harness path; the constructor refuses it (use the plain :8080 path).
func TestNewCertMagicConfig_DisabledConfigRejected(t *testing.T) {
	cfg := validTLSConfig()
	cfg.Disabled = true
	_, err := NewCertMagicConfig(context.Background(), cfg, "tok", quietLogger(), nil)
	if err == nil {
		t.Fatal("Disabled=true must be rejected by NewCertMagicConfig")
	}
	if !strings.Contains(err.Error(), "Disabled=true") {
		t.Errorf("expected 'Disabled=true' in error, got %v", err)
	}
}

// TestNewCertMagicConfig_HalfConfiguredRejected — a partial TOML (missing
// wildcard domain) must fail closed at Validate() time.
func TestNewCertMagicConfig_HalfConfiguredRejected(t *testing.T) {
	cfg := validTLSConfig()
	cfg.WildcardCertDomain = ""
	_, err := NewCertMagicConfig(context.Background(), cfg, "tok", quietLogger(), nil)
	if !errors.Is(err, ErrTLSMisconfigured) {
		t.Errorf("expected ErrTLSMisconfigured, got %v", err)
	}
}

// TestNewCertMagicConfig_NoAllowlistRejected — Disabled=false + no allowlist
// is the spec §11 abuse vector in reverse. The constructor must refuse to
// start so a misconfigured daemon can't mint certs for arbitrary hostnames.
func TestNewCertMagicConfig_NoAllowlistRejected(t *testing.T) {
	cfg := validTLSConfig()
	cfg.OnDemandHTTP01Allowlist = nil
	_, err := NewCertMagicConfig(context.Background(), cfg, "tok", quietLogger(), nil)
	if !errors.Is(err, ErrTLSAllowlistMissing) {
		t.Errorf("expected ErrTLSAllowlistMissing, got %v", err)
	}
}

// TestNewCertMagicConfig_StorageDirCreated — StorageDir doesn't exist; the
// constructor must MkdirAll it (per tls_wire.go:ensureStorageDir).
func TestNewCertMagicConfig_StorageDirCreated(t *testing.T) {
	h := newFakeHetzner(t)
	cfg := validTLSConfig()
	dir := testStorageDir(t) // under t.TempDir(); does not yet exist
	cfg.StorageDir = dir
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := NewCertMagicConfig(ctx, cfg, "tok", quietLogger(), newTestHetznerFactory(t, h)); err != nil {
		t.Fatalf("NewCertMagicConfig: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("storage dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("storage path %q is not a directory", dir)
	}
}

// TestCertMagicOnDemand_AbuseVectorDenied — **the spec §11 closure test.**
// Wire a CountingAllowlist that only permits "victim.example.com"; invoke
// the DecisionFunc with ServerName="attacker.example.com". Certmagic must
// short-circuit (allowlist denies → DecisionFunc returns non-nil error).
//
// We can't stand up a real listener for a real cert (we don't have a CA),
// so the test asserts the contract at the DecisionFunc callback level:
// invoke it directly and observe that the predicate denies + the counter
// recorded the deny.
func TestCertMagicOnDemand_AbuseVectorDenied(t *testing.T) {
	h := newFakeHetzner(t)
	counter := NewCountingAllowlist(StaticAllowlist("victim.example.com"))
	cfg := validTLSConfig()
	cfg.StorageDir = testStorageDir(t)
	cfg.OnDemandHTTP01Allowlist = counter.AsFunc()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	bundle, err := NewCertMagicConfig(ctx, cfg, "tok", quietLogger(), newTestHetznerFactory(t, h))
	if err != nil {
		t.Fatalf("NewCertMagicConfig: %v", err)
	}
	// DecisionFunc returns nil for allowlist hits and non-nil for denials.
	if err := bundle.DecisionFunc(ctx, "attacker.example.com"); err == nil {
		t.Fatal("DecisionFunc must deny 'attacker.example.com' (allowlist only permits 'victim.example.com')")
	}
	if got := counter.Deny.Load(); got < 1 {
		t.Errorf("counter.Deny = %d, want >= 1 (the deny path must be exercised)", got)
	}
	if got := counter.Allow.Load(); got != 0 {
		t.Errorf("counter.Allow = %d, want 0 (no allowlist hit expected for the attacker SNI)", got)
	}
}

// TestCertMagicOnDemand_KnownHostAllowed — the inverse of AbuseVectorDenied.
// DecisionFunc returns nil for the allowlisted host. We don't actually
// obtain a cert (no CA in the test); we just observe the predicate is
// called and produces the right bool.
func TestCertMagicOnDemand_KnownHostAllowed(t *testing.T) {
	h := newFakeHetzner(t)
	counter := NewCountingAllowlist(StaticAllowlist("victim.example.com"))
	cfg := validTLSConfig()
	cfg.StorageDir = testStorageDir(t)
	cfg.OnDemandHTTP01Allowlist = counter.AsFunc()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	bundle, err := NewCertMagicConfig(ctx, cfg, "tok", quietLogger(), newTestHetznerFactory(t, h))
	if err != nil {
		t.Fatalf("NewCertMagicConfig: %v", err)
	}
	if err := bundle.DecisionFunc(ctx, "victim.example.com"); err != nil {
		t.Errorf("DecisionFunc must allow 'victim.example.com': %v", err)
	}
	if got := counter.Allow.Load(); got != 1 {
		t.Errorf("counter.Allow = %d, want 1", got)
	}
}

// TestNewACMEMux_HTTPChallengeHandlerRoundTrip — wire NewACMEMux against the
// real bundle.HTTPChallengeHandler (not a hand-written mock). A GET to a
// path under /.well-known/acme-challenge/ must reach the handler (not
// redirect); a path outside that prefix must 308-redirect to https://.
func TestNewACMEMux_HTTPChallengeHandlerRoundTrip(t *testing.T) {
	h := newFakeHetzner(t)
	cfg := validTLSConfig()
	cfg.StorageDir = testStorageDir(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	bundle, err := NewCertMagicConfig(ctx, cfg, "tok", quietLogger(), newTestHetznerFactory(t, h))
	if err != nil {
		t.Fatalf("NewCertMagicConfig: %v", err)
	}
	mux := NewACMEMux(bundle.HTTPChallengeHandler)

	// ACME path: probe with an obviously-fake token; certmagic returns 404
	// (or 200 with a body) but does NOT redirect. Either is fine; we just
	// need to observe the handler was reached (not the redirect branch).
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/probe-token", nil)
	req.Host = "victim.example.com"
	mux.ServeHTTP(rec, req)
	if loc := rec.Header().Get("Location"); strings.HasPrefix(loc, "https://") {
		t.Errorf("ACME path must NOT 308-redirect; got Location=%q", loc)
	}

	// Non-ACME path: must 308 to https://<host><uri>.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Host = "victim.example.com"
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusPermanentRedirect {
		t.Errorf("non-ACME path: status = %d, want 308", rec2.Code)
	}
	if loc := rec2.Header().Get("Location"); loc != "https://victim.example.com/" {
		t.Errorf("Location = %q, want https://victim.example.com/", loc)
	}
}
