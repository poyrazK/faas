package state

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// TestLoadClusterSigningKey_EmptyTableReturnsNotFound pins the
// pre-bootstrap state: an operator who hasn't run
// `hostage-gen cluster-init` yet sees ErrNotFound, which the
// schedd minter + gatewayd-internal verifier translate into
// "fall back to per-host disk path". Without this guard the
// call sites would conflate "row missing" with "DB error" and
// fail-closed at boot, locking out single-box installs that
// don't yet have a cluster key.
func TestLoadClusterSigningKey_EmptyTableReturnsNotFound(t *testing.T) {
	pool := pgtest.Open(t)
	store := NewPgStore(pool)

	ctx := context.Background()
	if _, err := store.LoadClusterSigningKey(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty table: got %v, want ErrNotFound", err)
	}
}

// TestLoadClusterSigningKey_RoundTrip pins the basic happy
// path: insert → load → equal. The shape check on PublicKeyPEM
// + SealedBlob + KeyID is the load-bearing invariant — the
// minter-side cluster_key_loader reads these bytes and parses
// them as Ed25519; a flip of byte order or PEM block type
// would crash unseal.
//
// Uses a 32-byte random SealedBlob in place of real age
// ciphertext — the Store layer doesn't care about the wire
// format; pkg/secretbox owns that contract.
func TestLoadClusterSigningKey_RoundTrip(t *testing.T) {
	pool := pgtest.Open(t)
	store := NewPgStore(pool)

	ctx := context.Background()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}
	pemBytes, err := marshalPubPEM(pub)
	if err != nil {
		t.Fatalf("marshal pub PEM: %v", err)
	}
	sealed := make([]byte, 32)
	if _, err := rand.Read(sealed); err != nil {
		t.Fatalf("rand: %v", err)
	}
	k := ClusterSigningKey{
		KeyID:        deriveKidForTest(t, priv),
		PublicKeyPEM: string(pemBytes),
		SealedBlob:   sealed,
	}
	if err := store.InsertClusterSigningKey(ctx, k); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := store.LoadClusterSigningKey(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.KeyID != k.KeyID {
		t.Errorf("key_id mismatch: got %s want %s", got.KeyID, k.KeyID)
	}
	if got.PublicKeyPEM != k.PublicKeyPEM {
		t.Errorf("public_key_pem mismatch")
	}
	if string(got.SealedBlob) != string(k.SealedBlob) {
		t.Errorf("sealed_blob mismatch")
	}
	if got.CreatedAt.IsZero() {
		t.Errorf("created_at not populated by DEFAULT now()")
	}
	if got.RotatedAt != nil {
		t.Errorf("rotated_at should be NULL on first insert, got %v", *got.RotatedAt)
	}
	if got.RetiredAt != nil {
		t.Errorf("retired_at should be NULL on first insert, got %v", *got.RetiredAt)
	}
}

// TestInsertClusterSigningKey_SingletonRejectsSecondID pins
// the CHECK (id = 1) guard: a second INSERT with id=2 must
// fail at the table level. Without this, a buggy operator
// could split the fleet across multiple rows and break the
// "one cluster key" invariant.
func TestInsertClusterSigningKey_SingletonRejectsSecondID(t *testing.T) {
	pool := pgtest.Open(t)
	store := NewPgStore(pool)

	ctx := context.Background()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	pemBytes, err := marshalPubPEM(pub)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	sealed := make([]byte, 32)
	_, _ = rand.Read(sealed)
	first := ClusterSigningKey{
		KeyID:        "0000000000000000000000000000000000000000000000000000000000000000",
		PublicKeyPEM: string(pemBytes),
		SealedBlob:   sealed,
	}
	if err := store.InsertClusterSigningKey(ctx, first); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	// Direct INSERT with id=2 — bypasses the InsertClusterSigningKey
	// method (which always uses id=1) to test the CHECK constraint
	// at the table level.
	_, err = pool.Exec(ctx, `
		INSERT INTO public.cluster_signing_keys
		    (id, key_id, public_key_pem, sealed_blob)
		VALUES (2, '1111111111111111111111111111111111111111111111111111111111111111', $1, $2)
	`, string(pemBytes), sealed)
	if err == nil {
		t.Fatalf("second-row insert: expected CHECK (id = 1) to reject id=2")
	}
}

// TestInsertClusterSigningKey_RotationIsInPlace pins the
// ON CONFLICT DO UPDATE branch: a second InsertClusterSigningKey
// with a different key_id replaces the row in place. The
// retired_at stamp is set to now() so a future verifier-side
// rotation-overlap loader knows the previous kid is retired.
// PR-3 ships the table shape forward-compatible; the
// rotation-overlap READ path is a follow-on.
func TestInsertClusterSigningKey_RotationIsInPlace(t *testing.T) {
	pool := pgtest.Open(t)
	store := NewPgStore(pool)

	ctx := context.Background()
	pub1, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen 1: %v", err)
	}
	pub2, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen 2: %v", err)
	}
	pem1, err := marshalPubPEM(pub1)
	if err != nil {
		t.Fatalf("marshal 1: %v", err)
	}
	pem2, err := marshalPubPEM(pub2)
	if err != nil {
		t.Fatalf("marshal 2: %v", err)
	}
	sealed1 := make([]byte, 32)
	_, _ = rand.Read(sealed1)
	sealed2 := make([]byte, 32)
	_, _ = rand.Read(sealed2)

	kid1 := "0000000000000000000000000000000000000000000000000000000000000001"
	kid2 := "0000000000000000000000000000000000000000000000000000000000000002"

	if err := store.InsertClusterSigningKey(ctx, ClusterSigningKey{
		KeyID:        kid1,
		PublicKeyPEM: string(pem1),
		SealedBlob:   sealed1,
	}); err != nil {
		t.Fatalf("insert 1: %v", err)
	}
	if err := store.InsertClusterSigningKey(ctx, ClusterSigningKey{
		KeyID:        kid2,
		PublicKeyPEM: string(pem2),
		SealedBlob:   sealed2,
	}); err != nil {
		t.Fatalf("insert 2 (rotation): %v", err)
	}
	got, err := store.LoadClusterSigningKey(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.KeyID != kid2 {
		t.Errorf("rotation: expected kid=%s, got kid=%s", kid2, got.KeyID)
	}
	if got.PublicKeyPEM != string(pem2) {
		t.Errorf("rotation: public_key_pem not replaced")
	}
	if string(got.SealedBlob) != string(sealed2) {
		t.Errorf("rotation: sealed_blob not replaced")
	}
	if got.RetiredAt == nil {
		t.Errorf("rotation: expected retired_at to be set on replaced kid, got NULL")
	}
}

// TestDeleteClusterSigningKey_EmptyReturnsNotFound pins the
// pre-state: deleting from an empty table must return
// ErrNotFound, not a no-op success. The rollback path of the
// operator-bootstrap CLI distinguishes "nothing to roll back"
// (silent) from "rolled back the row you just inserted" (loud)
// using this signal.
func TestDeleteClusterSigningKey_EmptyReturnsNotFound(t *testing.T) {
	pool := pgtest.Open(t)
	store := NewPgStore(pool)

	ctx := context.Background()
	if err := store.DeleteClusterSigningKey(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete empty: got %v, want ErrNotFound", err)
	}
}

// TestDeleteClusterSigningKey_AfterInsert pins the happy-path
// round trip: insert → delete → load returns ErrNotFound.
func TestDeleteClusterSigningKey_AfterInsert(t *testing.T) {
	pool := pgtest.Open(t)
	store := NewPgStore(pool)

	ctx := context.Background()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	pemBytes, err := marshalPubPEM(pub)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	sealed := make([]byte, 32)
	_, _ = rand.Read(sealed)
	if err := store.InsertClusterSigningKey(ctx, ClusterSigningKey{
		KeyID:        "0000000000000000000000000000000000000000000000000000000000000003",
		PublicKeyPEM: string(pemBytes),
		SealedBlob:   sealed,
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := store.DeleteClusterSigningKey(ctx); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.LoadClusterSigningKey(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("load after delete: got %v, want ErrNotFound", err)
	}
}

// marshalPubPEM serialises an Ed25519 public key as the
// PEM-encoded PKIX form that cluster_signing_keys.public_key_pem
// stores. Mirrors the load-side parse in
// cmd/gatewayd-internal/internal_svc_verifier.go's
// newInternalSvcVerifierFromPEMs — the round-trip test fails
// loud if either side drifts from the canonical shape.
func marshalPubPEM(pub ed25519.PublicKey) ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), nil
}

// deriveKidForTest returns a valid kid (64 hex chars, satisfying
// the CHECK key_id ~ '^[a-f0-9]{64}$' constraint). The actual
// value isn't relevant to the round-trip test — only the shape
// matters — so a deterministic placeholder keeps the test
// independent of the canonical KidFromPub derivation in
// pkg/internalsvc. The cross-side round trip is asserted by
// cmd/schedd/cluster_key_loader_test.go (where pkg/internalsvc
// IS in scope), not here.
func deriveKidForTest(t *testing.T, priv ed25519.PrivateKey) string {
	t.Helper()
	_ = priv
	return "0000000000000000000000000000000000000000000000000000000000000000"
}
