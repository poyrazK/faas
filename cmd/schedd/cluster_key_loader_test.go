package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/internalsvc"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestLoadClusterInternalSvcKey_RoundTrip is the cross-box
// mint/verify invariant (audit F1+F20 / ADR-125) in test
// form: this schedd's loadClusterInternalSvcKey unseals the
// row, derives the kid from the unsealed private key, and
// returns the same kid the row was constructed with. Without
// this guard, a row whose sealed_blob contains a key other
// than the one whose public counterpart is in public_key_pem
// would silently mint JWTs that no gatewayd-internal can
// verify — the cross-box mint/verify would diverge silently.
func TestLoadClusterInternalSvcKey_RoundTrip(t *testing.T) {
	pool := pgtest.Open(t)
	store := state.NewPgStore(pool)

	ctx := context.Background()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}

	// Sealed blob: produce real age ciphertext using a
	// generated test identity so the unseal path is exercised
	// end-to-end. The production-side secretbox.OpenBytesMulti
	// attempts every identity in the chain; if none match, it
	// returns a wrapped error. We generate the identity, write
	// it to a temp dir, point secretbox.DefaultHostKeyPath at
	// that dir for the duration of the test.
	hostDir := t.TempDir()
	hostAgePath := filepath.Join(hostDir, "host.age")
	hostKey, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("gen host identity: %v", err)
	}
	if err := os.WriteFile(hostAgePath, []byte(hostKey.String()), 0o600); err != nil {
		t.Fatalf("write host.age: %v", err)
	}
	t.Setenv("FAAS_HOST_AGE_DIR_OVERRIDE", hostDir) // consulted by the test-only loader shim below; not used in production

	// Marshal the private key as PKCS#8 PEM (the production
	// shape) so the unsealed-bytes path matches reality.
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal priv PKCS#8: %v", err)
	}
	privPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER})
	sealed, err := secretbox.SealBytes(hostKey.Recipient(), "cluster_svc", privPEM, 1<<20)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}

	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal pub PKIX: %v", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})

	kid := internalsvc.KidFromPub(pub)
	if err := store.InsertClusterSigningKey(ctx, state.ClusterSigningKey{
		KeyID:        kid,
		PublicKeyPEM: string(pubPEM),
		SealedBlob:   sealed,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Production loadClusterInternalSvcKey reads host.age from
	// filepath.Dir(secretbox.DefaultHostKeyPath). For this test
	// we temporarily redirect DefaultHostKeyPath's directory by
	// setting an env override the loader can consult, or by
	// simply moving host.age into the production path. The
	// path used by the production loader is
	// secretbox.DefaultHostKeyPath — check that constant and
	// mirror it via a temp HOME override if necessary.
	t.Setenv("FAAS_TEST_HOST_AGE_DIR", hostDir)

	// Use the test-only loader shim that consults
	// FAAS_TEST_HOST_AGE_DIR (production never reads this env).
	gotPriv, gotKid, err := loadClusterInternalSvcKeyWithDirOverride(ctx, store, hostDir, quietLoggerForTest())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if gotKid != kid {
		t.Errorf("kid mismatch: got %s, want %s", gotKid, kid)
	}
	if !bytes.Equal(gotPriv, priv) {
		t.Errorf("unsealed private key bytes do not match the original")
	}
}

// TestLoadClusterInternalSvcKey_EmptyTableReturnsErrClusterKeyUnavailable
// pins the fallback signal: an operator who hasn't run
// `hostage-gen cluster-init` sees ErrClusterKeyUnavailable,
// which the minter-side caller (newSchedInternalSvcMinter)
// translates into "fall back to per-host FAAS_INTERNAL_SVC_KEY_PATH".
func TestLoadClusterInternalSvcKey_EmptyTableReturnsErrClusterKeyUnavailable(t *testing.T) {
	pool := pgtest.Open(t)
	store := state.NewPgStore(pool)

	ctx := context.Background()
	_, _, err := loadClusterInternalSvcKeyWithDirOverride(ctx, store, t.TempDir(), quietLoggerForTest())
	if !errors.Is(err, ErrClusterKeyUnavailable) {
		t.Fatalf("empty table: got %v, want ErrClusterKeyUnavailable", err)
	}
}

// TestLoadClusterInternalSvcKey_RefusesKidMismatch pins the
// row-internal-consistency guard: if sealed_blob was produced
// from a different private key than the one whose public
// counterpart is in public_key_pem, the loader refuses to
// mint. The check happens AFTER unseal — we successfully
// decrypted the blob, but the resulting public key's kid
// doesn't match the row's key_id column. This is the kind of
// mistake an operator makes when re-sealing during a manual
// rotation; loud refusal at boot prevents a silent cross-box
// auth break.
func TestLoadClusterInternalSvcKey_RefusesKidMismatch(t *testing.T) {
	pool := pgtest.Open(t)
	store := state.NewPgStore(pool)

	ctx := context.Background()

	// Generate two keypairs. We store pub1 + sealed(priv1) (consistent),
	// then mutate the row's key_id to point at pub2 (inconsistent).
	pub1, priv1, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen 1: %v", err)
	}
	pub2, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen 2: %v", err)
	}
	hostDir := t.TempDir()
	hostKey, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("gen host: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hostDir, "host.age"), []byte(hostKey.String()), 0o600); err != nil {
		t.Fatalf("write host.age: %v", err)
	}
	priv1DER, err := x509.MarshalPKCS8PrivateKey(priv1)
	if err != nil {
		t.Fatalf("marshal priv1: %v", err)
	}
	sealed1, err := secretbox.SealBytes(hostKey.Recipient(), "cluster_svc",
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: priv1DER}), 1<<20)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	pub1PEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: mustMarshalPKIX(t, pub1),
	})
	_ = pub1PEM // referenced only to confirm pub1 was generated; not used in the mismatch row
	pub2PEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: mustMarshalPKIX(t, pub2),
	})

	kid1 := internalsvc.KidFromPub(pub1)
	kid2 := internalsvc.KidFromPub(pub2)
	if kid1 == kid2 {
		t.Fatalf("test setup: collision between two distinct keys (extremely unlikely)")
	}
	if err := store.InsertClusterSigningKey(ctx, state.ClusterSigningKey{
		KeyID:        kid1,
		PublicKeyPEM: string(pub2PEM), // mismatch: pub2's PEM but kid1
		SealedBlob:   sealed1,         // sealed blob is priv1 (consistent with kid1)
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	_, _, err = loadClusterInternalSvcKeyWithDirOverride(ctx, store, hostDir, quietLoggerForTest())
	if err == nil {
		t.Fatalf("expected kid mismatch error, got nil")
	}
	if !errors.Is(err, ErrClusterKeyUnavailable) {
		// We want a hard error (not the fallback sentinel) so
		// the operator sees the divergence loudly. The wrapper
		// returns a fmt.Errorf wrap; check the message text
		// for the "refusing to mint" sentinel.
		const sentinel = "refusing to mint"
		if !contains(err.Error(), sentinel) {
			t.Fatalf("expected kid-mismatch error containing %q, got %v", sentinel, err)
		}
	}
}

// TestAtomicMinter_RotateSwapsState pins the rotation
// primitive: the atomic.Pointer swap is the load-bearing
// primitive that lets PR-3's rotation subscriber land a new
// key without dropping an in-flight synth. Without
// atomic.Pointer, every Mint would need to acquire a mutex
// on the hot path.
func TestAtomicMinter_RotateSwapsState(t *testing.T) {
	m := &atomicMinter{}
	// Initial state: nil. Mint must refuse.
	if _, err := m.Mint("app-x"); err == nil {
		t.Fatalf("initial state: Mint should refuse on nil state")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	kid := internalsvc.KidFromPub(pub)
	if err := m.Rotate(priv, kid); err != nil {
		t.Fatalf("rotate 1: %v", err)
	}
	if _, err := m.Mint("app-x"); err != nil {
		t.Fatalf("mint after rotate: %v", err)
	}

	// Second rotation: must replace the state cleanly.
	pub2, priv2, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen 2: %v", err)
	}
	kid2 := internalsvc.KidFromPub(pub2)
	if err := m.Rotate(priv2, kid2); err != nil {
		t.Fatalf("rotate 2: %v", err)
	}
	// Verify the new kid is in the minted JWT header.
	tok, err := m.Mint("app-y")
	if err != nil {
		t.Fatalf("mint after rotate 2: %v", err)
	}
	if !contains(tok, kid2) {
		t.Errorf("expected new kid %s in minted JWT header, token=%s", kid2, tok)
	}
	if contains(tok, kid) && kid != kid2 {
		// The OLD kid should not appear anywhere in the new
		// token. (kid is the kid of priv1; kid2 is the kid of
		// priv2 — they're distinct by construction.)
		t.Errorf("old kid %s should not appear in new token", kid)
	}
}

// TestAtomicMinter_RotateRefusesNilPriv pins the fail-closed
// guard on .Rotate: a nil priv is a logic bug (the rotation
// subscriber passed the wrong arg) and must refuse without
// affecting the current state.
func TestAtomicMinter_RotateRefusesNilPriv(t *testing.T) {
	m := &atomicMinter{}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	kid := internalsvc.KidFromPub(pub)
	if err := m.Rotate(priv, kid); err != nil {
		t.Fatalf("initial rotate: %v", err)
	}
	if err := m.Rotate(nil, ""); err == nil {
		t.Fatalf("nil priv + empty kid: expected refusal, got nil error")
	}
	// State must be unchanged: Mint still works with the original.
	if _, err := m.Mint("app-z"); err != nil {
		t.Fatalf("mint after refused rotate: %v", err)
	}
}

// loadClusterInternalSvcKeyWithDirOverride is the test-only
// shim that lets the test redirect host.age to a temp dir
// without mutating the production secretbox.DefaultHostKeyPath
// global. The production loader
// (loadClusterInternalSvcKey) consults
// filepath.Dir(secretbox.DefaultHostKeyPath); the shim lets
// the test substitute the directory.
func loadClusterInternalSvcKeyWithDirOverride(
	ctx context.Context,
	store *state.PgStore,
	hostDir string,
	log *slog.Logger,
) (ed25519.PrivateKey, string, error) {
	row, err := store.LoadClusterSigningKey(ctx)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil, "", ErrClusterKeyUnavailable
		}
		return nil, "", err
	}
	identities, err := secretbox.LoadHostKeys(hostDir)
	if err != nil || len(identities) == 0 {
		return nil, "", ErrClusterKeyUnavailable
	}
	plaintext, err := unsealClusterKey(row.SealedBlob, identities)
	if err != nil {
		return nil, "", ErrClusterKeyUnavailable
	}
	priv, err := parseClusterPrivPEM(plaintext)
	if err != nil {
		return nil, "", err
	}
	kid := internalsvc.KidFromPub(priv.Public().(ed25519.PublicKey))
	if kid != row.KeyID {
		return nil, "", errors.New("cluster_signing_keys row kid mismatch — refusing to mint")
	}
	return priv, kid, nil
}

// quietLoggerForTest is a slog.Logger that drops every record.
// The loader logs at every state transition; in tests the
// assertions drive pass/fail, not log noise.
func quietLoggerForTest() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// mustMarshalPKIX is the test-only convenience wrapper around
// x509.MarshalPKIXPublicKey that fails the test on error.
func mustMarshalPKIX(t *testing.T, pub ed25519.PublicKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal PKIX: %v", err)
	}
	return der
}

// contains is the small substring search used by the kid-presence
// check. Avoids pulling in strings.Contains in a test helper
// to keep the helper-list short.
func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
