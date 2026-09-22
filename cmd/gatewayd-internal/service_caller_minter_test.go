// adr: 206
package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/servicecaller"
	"github.com/onebox-faas/faas/pkg/state"
)

// Off by default: the assertion has no consumer yet, so an operator who has
// not opted in must not pay for a signature on every internal call.
func TestServiceCallerMinterDisabledByDefault(t *testing.T) {
	t.Setenv(serviceCallerKeyPathEnv, filepath.Join(t.TempDir(), "key.pem"))
	if got := newServiceCallerMinter(context.Background(), state.NewMemStore(), "node-1", testLogger()); got != nil {
		t.Error("minter was returned with the feature flag unset")
	}
}

func TestServiceCallerMinterMintsVerifiableAssertion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gatewayd.ed25519")
	t.Setenv(serviceCallerEnabledEnv, "1")
	t.Setenv(serviceCallerKeyPathEnv, path)

	mint := newServiceCallerMinter(context.Background(), state.NewMemStore(), "node-1", testLogger())
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
	if newServiceCallerMinter(context.Background(), state.NewMemStore(), "node-1", testLogger()) != nil {
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

// An assertion is minted by the caller's node and verified on the target's
// node, so a node that never publishes its public half produces assertions no
// peer can check. Enabling the feature must publish.
func TestServiceCallerMinterPublishesPublicKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gatewayd.ed25519")
	t.Setenv(serviceCallerEnabledEnv, "1")
	t.Setenv(serviceCallerKeyPathEnv, path)
	store := state.NewMemStore()

	if newServiceCallerMinter(context.Background(), store, "node-a", testLogger()) == nil {
		t.Fatal("minter is nil")
	}
	keys, err := store.ListServiceCallerKeys(context.Background())
	if err != nil {
		t.Fatalf("list keys: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("published %d keys, want 1", len(keys))
	}
	priv := readKey(t, path)
	if keys[0].NodeID != "node-a" || keys[0].KeyID != serviceCallerKid(priv) {
		t.Errorf("published %+v, want node-a with the on-disk kid", keys[0])
	}
	// The PEM must satisfy the service_caller_keys CHECK, or publication
	// succeeds in MemStore and fails against Postgres.
	if !strings.HasPrefix(keys[0].PublicKeyPEM, "-----BEGIN PUBLIC KEY-----") {
		t.Errorf("published PEM = %.40q, want a PKIX PUBLIC KEY block", keys[0].PublicKeyPEM)
	}
}

// Publication is best-effort: a store failure must not stop the node serving
// traffic for a feature nothing consumes yet.
func TestServiceCallerMinterSurvivesPublishFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gatewayd.ed25519")
	t.Setenv(serviceCallerEnabledEnv, "1")
	t.Setenv(serviceCallerKeyPathEnv, path)

	mint := newServiceCallerMinter(context.Background(), failingKeyStore{}, "node-a", testLogger())
	if mint == nil {
		t.Fatal("minter is nil after a publish failure; the node stopped minting over a best-effort step")
	}
}

type failingKeyStore struct{}

func (failingKeyStore) PublishServiceCallerKey(context.Context, state.ServiceCallerKey) error {
	return errors.New("store unavailable")
}

func (failingKeyStore) ListServiceCallerKeys(context.Context) ([]state.ServiceCallerKey, error) {
	return nil, errors.New("store unavailable")
}
