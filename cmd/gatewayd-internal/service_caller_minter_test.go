// adr: 206
package main

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/servicecaller"
)

// Off by default: the assertion has no consumer yet, so an operator who has
// not opted in must not pay for a signature on every internal call.
func TestServiceCallerMinterDisabledByDefault(t *testing.T) {
	t.Setenv(serviceCallerKeyPathEnv, filepath.Join(t.TempDir(), "key.pem"))
	if got := newServiceCallerMinter("node-1", testLogger()); got != nil {
		t.Error("minter was returned with the feature flag unset")
	}
}

func TestServiceCallerMinterMintsVerifiableAssertion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gatewayd.ed25519")
	t.Setenv(serviceCallerEnabledEnv, "1")
	t.Setenv(serviceCallerKeyPathEnv, path)

	mint := newServiceCallerMinter("node-1", testLogger())
	if mint == nil {
		t.Fatal("minter is nil with the flag on")
	}
	token, err := mint(gateway.ServiceCallerMintInput{
		CallerAppID: "caller-app", TargetAppID: "target-app",
		AccountID: "acct-1", CallerInstanceID: "inst-1",
	})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	// Load the key back and verify, which is what a consumer will do.
	priv := readKey(t, path)
	keys := servicecaller.TrustedKeys{serviceCallerKid(priv): priv.Public().(ed25519.PublicKey)}
	got, err := servicecaller.Verify(token, keys, "target-app", time.Now())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.CallerAppID != "caller-app" {
		t.Errorf("CallerAppID = %q, want caller-app", got.CallerAppID)
	}
	if got.AccountID != "acct-1" {
		t.Errorf("AccountID = %q, want acct-1", got.AccountID)
	}
}

// First boot must produce a usable key rather than failing closed, and the
// same key must yield the same kid across restarts — otherwise every restart
// would invalidate assertions a verifier had just learned to trust.
func TestServiceCallerKeyIsStableAcrossLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gatewayd.ed25519")
	t.Setenv(serviceCallerKeyPathEnv, path)

	first, firstKid, err := loadServiceCallerKey(testLogger())
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	second, secondKid, err := loadServiceCallerKey(testLogger())
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if firstKid != secondKid {
		t.Errorf("kid changed across loads: %q then %q", firstKid, secondKid)
	}
	if !first.Equal(second) {
		t.Error("key material changed across loads; the generated key was not persisted")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat key: %v", err)
	}
	// The private half of an identity assertion must not be world-readable.
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("key mode = %o, want 600", perm)
	}
}

// A malformed key must not silently degrade to an unsigned mesh without
// saying so; newServiceCallerMinter returns nil (calls continue) and the
// loader surfaces the reason.
func TestServiceCallerKeyRejectsGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gatewayd.ed25519")
	if err := os.WriteFile(path, []byte("not a pem key"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv(serviceCallerKeyPathEnv, path)

	if _, _, err := loadServiceCallerKey(testLogger()); err == nil {
		t.Fatal("loader accepted a non-PEM key")
	}
	t.Setenv(serviceCallerEnabledEnv, "1")
	if newServiceCallerMinter("node-1", testLogger()) != nil {
		t.Error("minter was returned despite an unusable key")
	}
}

func readKey(t *testing.T, path string) ed25519.PrivateKey {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	priv, err := parseServiceCallerKeyPEM(data)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}
	return priv
}
