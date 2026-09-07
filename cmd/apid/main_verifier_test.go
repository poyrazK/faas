// Tests for the PR-B (issue #678 / ADR-056) verifier wiring in
// cmd/apid/main.go. Sister file: main_test.go (runWithDeps lifecycle).
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/wire"
)

// preLoadedConfigForVerifierTest builds a minimal Config that
// populates the three TLS clusters (githubd client, advisory server,
// githubd-bridge server) using the same PKI material so each
// Load*TLSWithVerifier call returns a real *tls.Config that the
// verifier hook can install on. Without the populated cluster,
// wire.LoadClientTLSConfig returns (nil, nil) — the single-box
// back-compat path — and the hook never installs.
func preLoadedConfigForVerifierTest(t *testing.T) *Config {
	t.Helper()
	dir := t.TempDir()
	ca, cert, key := writePKIMaterial(t, dir)
	return &Config{
		// Enable all three listeners so the Load*TLSWithVerifier
		// calls fire. Pre-PR-B env-var fallbacks resolve the same
		// fields, but preLoadedConfig bypasses both the env helpers
		// and the TOML round-trip — direct struct fields are enough.
		GithubdClientTLSCAPath:   ca,
		GithubdClientTLSCertPath: cert,
		GithubdClientTLSKeyPath:  key,
		AdvisoryTLSCAPath:        ca,
		AdvisoryTLSCertPath:      cert,
		AdvisoryTLSKeyPath:       key,
		GithubdBridgeTLSCAPath:   ca,
		GithubdBridgeTLSCertPath: cert,
		GithubdBridgeTLSKeyPath:  key,
		// NodeName stays "" — preLoadedNodeVerifier bypasses the
		// gate. The stub verifier flows through Load*TLSWithVerifier
		// regardless.
	}
}

// TestRunWithDeps_PassesNodeVerifierToDialSites pins the PR-B
// contract: when a non-nil preLoadedNodeVerifier is set on runDeps,
// the handshake-layer hook reaches the unconditional githubd dial
// site via cfg.LoadGithubdTLSWithVerifier(nodeVerifier). The test
// uses the captureDialTLS seam — added in PR-B — to record the
// *tls.Config the wire factory produces at each dial site, then
// asserts the captured githubd *tls.Config carries a non-nil
// VerifyPeerCertificate.
//
// Strategy: invoke runWithDeps with a listen stub that errors at
// bind time. The verifier construction block + all three
// Load*TLSWithVerifier calls happen BEFORE deps.listen is reached,
// so by the time runWithDeps returns the expected error, every
// captured tlsCfg is observable. The advisory + bridge listeners
// are also captured IF the resolved sock is non-empty (default
// preLoadedConfig has the socks empty; the test does NOT override
// them, so advisory + bridge calls are skipped — that's the
// pre-PR-B single-box back-compat shape).
func TestRunWithDeps_PassesNodeVerifierToDialSites(t *testing.T) {
	withTestHMACFiles(t)
	withBillingKeysForTest(t)
	withTestMailTransport(t)

	stub := &captureTestVerifier{}

	var (
		mu       sync.Mutex
		captured = map[string]*tls.Config{}
	)

	deps := defaultDeps()
	withTestStore(&deps)
	deps.preLoadedConfig = preLoadedConfigForVerifierTest(t)
	deps.config = deps.preLoadedConfig // runWithDeps reads deps.config, not deps.preLoadedConfig
	// Hand the test a stub verifier. preLoadedNodeVerifier overrides
	// the cfg.NodeName gate inside runWithDeps, so even with
	// preLoadedConfig.NodeName == "" the verifier flows through.
	deps.preLoadedNodeVerifier = stub
	// Capture every dial-site *tls.Config. nil-tolerated on the
	// receiving end (runWithDeps no-ops when captureDialTLS is nil).
	deps.captureDialTLS = func(name string, cfg *tls.Config) {
		mu.Lock()
		captured[name] = cfg
		mu.Unlock()
	}
	// Make the main bind fail so runWithDeps returns quickly after
	// exercising the verifier block + all Load*TLSWithVerifier
	// calls. Every captured dial-site tlsCfg is observable before
	// this error fires.
	deps.listen = func(_, _ string) (net.Listener, error) {
		return nil, errors.New("bind aborted")
	}

	err := runWithDeps(context.Background(), discardLogger(), deps)
	if err == nil || err.Error() != "bind aborted" {
		// The bind failure is the expected signal; other errors mean
		// runWithDeps tripped earlier (verifier construction, TLS
		// load, store open, etc.).
		t.Fatalf("runWithDeps: err=%v, want bind aborted", err)
	}

	mu.Lock()
	defer mu.Unlock()
	// The githubd dial is unconditional in runWithDeps (no sock
	// gate). It must have been captured AND the hook must be
	// installed.
	ghCfg, ok := captured["githubd"]
	if !ok {
		t.Fatal("githubd dial-site tlsCfg not captured — verify captureDialTLS seam fired")
	}
	if ghCfg == nil {
		t.Fatal("githubd: captured nil *tls.Config")
	}
	if ghCfg.VerifyPeerCertificate == nil {
		t.Fatal("githubd: VerifyPeerCertificate = nil, want non-nil (verifier installed)")
	}
	// The advisory + bridge calls fire only when the resolved sock
	// is non-empty. With preLoadedConfig leaving AdvisorySock /
	// GithubdBridgeSock empty (the env-only enable path),
	// resolveAdvisorySock / resolveGithubdBridgeSock return "" and
	// the Load*TLSWithVerifier block is skipped. Pin the absence so
	// a future refactor that flips the gate is visible.
	if _, ok := captured["advisory"]; ok {
		t.Errorf("advisory captured (AdvisorySock empty in preLoadedConfig; expected skip)")
	}
	if _, ok := captured["bridge"]; ok {
		t.Errorf("bridge captured (GithubdBridgeSock empty in preLoadedConfig; expected skip)")
	}
}

// TestRunWithDeps_NilPreLoadedNodeVerifier_NoHookInstalled is the
// negative control: with preLoadedNodeVerifier == nil AND
// cfg.NodeName == "" (the default), no verifier is constructed and
// the wire factory's setVerifyHook no-ops. The captured githubd
// tlsCfg MUST have a nil VerifyPeerCertificate — this pins the
// single-box back-compat path that PR-B is careful not to regress.
func TestRunWithDeps_NilPreLoadedNodeVerifier_NoHookInstalled(t *testing.T) {
	withTestHMACFiles(t)
	withBillingKeysForTest(t)
	withTestMailTransport(t)

	var (
		mu       sync.Mutex
		captured = map[string]*tls.Config{}
	)

	deps := defaultDeps()
	withTestStore(&deps)
	deps.preLoadedConfig = preLoadedConfigForVerifierTest(t)
	deps.config = deps.preLoadedConfig // runWithDeps reads deps.config, not deps.preLoadedConfig
	deps.preLoadedNodeVerifier = nil   // explicit
	deps.captureDialTLS = func(name string, cfg *tls.Config) {
		mu.Lock()
		captured[name] = cfg
		mu.Unlock()
	}
	deps.listen = func(_, _ string) (net.Listener, error) {
		return nil, errors.New("bind aborted")
	}

	err := runWithDeps(context.Background(), discardLogger(), deps)
	if err == nil || err.Error() != "bind aborted" {
		t.Fatalf("runWithDeps: err=%v, want bind aborted", err)
	}

	mu.Lock()
	defer mu.Unlock()
	ghCfg, ok := captured["githubd"]
	if !ok {
		t.Fatal("githubd dial-site tlsCfg not captured")
	}
	if ghCfg == nil {
		t.Fatal("githubd: captured nil *tls.Config")
	}
	if ghCfg.VerifyPeerCertificate != nil {
		t.Errorf("githubd: VerifyPeerCertificate = %p, want nil (single-box back-compat)", ghCfg.VerifyPeerCertificate)
	}
}

// captureTestVerifier is a hand-rolled wire.NodeVerifier that
// records every CN it is asked to look up. Distinct from the
// recordingVerifier in config_verifier_test.go so each test file's
// helper surface is self-contained.
type captureTestVerifier struct {
	mu  sync.Mutex
	CNs []string
}

func (v *captureTestVerifier) LookupCN(cn string) error {
	v.mu.Lock()
	v.CNs = append(v.CNs, cn)
	v.mu.Unlock()
	return nil
}

// silentVerifier returns nil unconditionally without recording any
// CN. Used to exercise the AllowAll-style "accept every peer" path
// without polluting the captured-CNs slice.
type silentVerifier struct{}

func (*silentVerifier) LookupCN(string) error { return nil }

// Ensure both stub types satisfy wire.NodeVerifier at compile time.
// A forgotten method would fail here, not at the first dial.
var (
	_ wire.NodeVerifier = (*captureTestVerifier)(nil)
	_ wire.NodeVerifier = (*silentVerifier)(nil)
)
