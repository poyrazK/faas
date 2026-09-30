package state

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
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
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
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
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
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
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
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
		KeyID:        "AAAAAAAAAAAAAAAAAAAAAA",
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
		INSERT INTO cluster_signing_keys
		    (id, key_id, public_key_pem, sealed_blob)
		VALUES (2, 'BBBBBBBBBBBBBBBBBBBBB', $1, $2)
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
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
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

	kid1 := "AAAAAAAAAAAAAAAAAAAAAB"
	kid2 := "AAAAAAAAAAAAAAAAAAAAAC"

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
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := NewPgStore(pool)

	ctx := context.Background()
	if err := store.DeleteClusterSigningKey(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete empty: got %v, want ErrNotFound", err)
	}
}

// TestDeleteClusterSigningKey_AfterInsert pins the happy-path
// round trip: insert → delete → load returns ErrNotFound.
func TestDeleteClusterSigningKey_AfterInsert(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
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
		KeyID:        "AAAAAAAAAAAAAAAAAAAAAD",
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

// TestInsertClusterSigningKey_IdempotentReInsertSameKid pins
// the rotation-overlap branch of the ON CONFLICT DO UPDATE
// retired_at clause:
//
//	retired_at = CASE
//	    WHEN cluster_signing_keys.key_id = EXCLUDED.key_id
//	        THEN cluster_signing_keys.retired_at  -- idempotent re-insert
//	        ELSE now()                              -- rotation: previous kid retired
//	END
//
// A second INSERT with the SAME kid must NOT bump retired_at
// to now() (it would otherwise look like a rotation to verifiers
// that read the row). Without this guard, an operator running
// `hostage-gen cluster-init` twice in a row would mark the
// canonical kid as retired and lock out the gateway until the
// next INSERT landed.
//
// Note on rotated_at: the SQL sets rotated_at = COALESCE(prev, now())
// on every re-insert — i.e. the FIRST non-NULL timestamp is preserved.
// The test does NOT assert rotated_at stays NULL because the COALESCE
// branch stamps it on the first re-insert by design; the test only
// pins the retired_at idempotency invariant.
//
// Asserted invariants:
//   - retired_at stays NULL (no rotation happened — the load-bearing
//     idempotent re-insert contract)
//   - sealed_blob bytes are replaced (the re-insert took effect;
//     a stale-row aliasing bug would fail this)
//   - created_at is preserved across the re-insert (no reset;
//     a DELETE-then-INSERT in the ON CONFLICT branch would fail this)
func TestInsertClusterSigningKey_IdempotentReInsertSameKid(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
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
	sealed1 := make([]byte, 32)
	_, _ = rand.Read(sealed1)
	sealed2 := make([]byte, 32)
	_, _ = rand.Read(sealed2)

	kid := "AAAAAAAAAAAAAAAAAAAAAA"
	first := ClusterSigningKey{KeyID: kid, PublicKeyPEM: string(pemBytes), SealedBlob: sealed1}
	if err := store.InsertClusterSigningKey(ctx, first); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	got, err := store.LoadClusterSigningKey(ctx)
	if err != nil {
		t.Fatalf("load after first: %v", err)
	}
	createdAtFirst := got.CreatedAt
	if got.RetiredAt != nil {
		t.Fatalf("first insert: expected retired_at NULL, got %v", *got.RetiredAt)
	}

	// Re-insert with the SAME kid but a different sealed_blob.
	// The idempotent branch must replace sealed_blob without
	// stamping retired_at to now() (the load-bearing invariant).
	second := ClusterSigningKey{KeyID: kid, PublicKeyPEM: string(pemBytes), SealedBlob: sealed2}
	if err := store.InsertClusterSigningKey(ctx, second); err != nil {
		t.Fatalf("re-insert same kid: %v", err)
	}
	got, err = store.LoadClusterSigningKey(ctx)
	if err != nil {
		t.Fatalf("load after re-insert: %v", err)
	}
	if string(got.SealedBlob) != string(sealed2) {
		t.Errorf("re-insert: sealed_blob not replaced")
	}
	if got.RetiredAt != nil {
		t.Errorf("re-insert same kid: expected retired_at NULL (idempotent branch), got %v",
			*got.RetiredAt)
	}
	if !got.CreatedAt.Equal(createdAtFirst) {
		t.Errorf("re-insert: created_at was reset (was %v, now %v) — ON CONFLICT branch deleted the row",
			createdAtFirst, got.CreatedAt)
	}
}

// TestInsertClusterSigningKey_RejectsEmptyFields pins the
// pre-Postgres input-validation guard. Without this, an operator
// who forgets to populate one of key_id / public_key_pem /
// sealed_blob would push the malformed row to goose's INSERT and
// crash on the table CHECK constraints (kid shape, PEM header)
// with a less actionable error message. Loud refuse-early means
// the operator CLI can show a targeted error instead of a SQL
// traceback.
func TestInsertClusterSigningKey_RejectsEmptyFields(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
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

	cases := []struct {
		name string
		key  ClusterSigningKey
	}{
		{"empty_key_id", ClusterSigningKey{KeyID: "", PublicKeyPEM: string(pemBytes), SealedBlob: sealed}},
		{"empty_public_key_pem", ClusterSigningKey{KeyID: deriveKidForTest(t, nil), PublicKeyPEM: "", SealedBlob: sealed}},
		{"empty_sealed_blob", ClusterSigningKey{KeyID: deriveKidForTest(t, nil), PublicKeyPEM: string(pemBytes), SealedBlob: nil}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := store.InsertClusterSigningKey(ctx, tc.key); err == nil {
				t.Fatalf("%s: expected empty-field refusal, got nil", tc.name)
			}
		})
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

// deriveKidForTest returns a valid kid (22-char base64url string,
// satisfying the CHECK key_id ~ '^[A-Za-z0-9_-]{22}$' constraint).
// The actual value isn't relevant to the round-trip test — only the
// shape matters — so a deterministic placeholder keeps the test
// independent of the canonical KidFromPub derivation in
// pkg/internalsvc. The cross-side round trip is asserted by
// cmd/schedd/cluster_key_loader_test.go (where pkg/internalsvc IS in
// scope), not here.
func deriveKidForTest(t *testing.T, priv ed25519.PrivateKey) string {
	t.Helper()
	_ = priv
	return "AAAAAAAAAAAAAAAAAAAAAA"
}

// CreateClusterSigningKeyIfAbsent is the new-fleet bootstrap used by
// fleet-seal migrate. It must create the row once and never overwrite an
// existing key (a concurrent first migration, or a later rotation).
func TestCreateClusterSigningKeyIfAbsent_CreatesOnceNeverOverwrites(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := NewPgStore(pool)
	ctx := context.Background()

	first := ClusterSigningKey{KeyID: strings.Repeat("A", 22), PublicKeyPEM: "first", SealedBlob: []byte("first-blob")}
	created, err := store.CreateClusterSigningKeyIfAbsent(ctx, first)
	if err != nil || !created {
		t.Fatalf("first create: created=%v err=%v", created, err)
	}
	second := ClusterSigningKey{KeyID: strings.Repeat("B", 22), PublicKeyPEM: "second", SealedBlob: []byte("second-blob")}
	created, err = store.CreateClusterSigningKeyIfAbsent(ctx, second)
	if err != nil || created {
		t.Fatalf("second create: created=%v err=%v, want false/nil", created, err)
	}
	got, err := store.LoadClusterSigningKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.KeyID != first.KeyID || got.PublicKeyPEM != first.PublicKeyPEM || string(got.SealedBlob) != "first-blob" {
		t.Fatalf("row was overwritten: %+v", got)
	}
	if _, err := store.CreateClusterSigningKeyIfAbsent(ctx, ClusterSigningKey{}); err == nil {
		t.Fatal("empty key was accepted")
	}
}
