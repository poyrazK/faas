// pgstore_cluster_keys.go — Store methods for the cluster_signing_keys
// table (migration 00351, PR-3 / audit F1+F20 / ADR-125).
//
// Why this lives in pkg/state, not pkg/internalsvc: the unseal +
// verify logic in pkg/internalsvc is pure (Ed25519 only, no
// Postgres dependency). Adding a *pgxpool.Pool to pkg/internalsvc
// would invert the dependency direction (today internalsvc is a
// leaf — no pgx, no apid, no migrations). The Store layer keeps
// the unseal story out of the wire-format package and confines
// SQL to pkg/state, matching the project's CLAUDE.md "SQL via
// sqlc only; no string-built queries" convention (raw pgxpool.Exec
// here — there is no sqlc.yaml in this repo, the convention is
// raw pgxpool.Exec / Query / QueryRow inside pkg/state/pgstore*.go
// with a `state: ...` wrap).
//
// Three surface methods:
//
//   - LoadClusterSigningKey    — read the singleton row; returns
//     ErrNotFound if the table is empty (the operator-migration
//     window before `hostage-gen cluster-init` runs).
//   - InsertClusterSigningKey  — write the singleton row; used by
//     the operator bootstrap command (out-of-scope here, but the
//     method is the in-tree helper that any future CLI calls).
//     ON CONFLICT (id) DO UPDATE keeps the singleton invariant and
//     supports the rotation protocol (insert with new key; the
//     previous row is updated in place to retired_at = now()).
//   - DeleteClusterSigningKey  — drop the row; used by tests and
//     the rollback path of `hostage-gen cluster-init`.

package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ClusterSigningKey is the in-memory representation of a row in
// public.cluster_signing_keys. KeyID is the kid header schedd
// stamps on every minted JWT (matches pkg/internalsvc.KidFromPub
// when the public key is the unsealed counterpart of the
// private key inside SealedBlob). PublicKeyPEM is plaintext
// (gatewayd-internal needs it without unsealing). SealedBlob is
// opaque age ciphertext owned by pkg/secretbox — pass it through
// to secretbox.OpenBytesMulti to recover the PEM-encoded
// PKCS#8 Ed25519 private key.
//
// CreatedAt / RotatedAt / RetiredAt mirror the table columns.
// RetiredAt non-null + non-future means the key is in its
// rotation-overlap grace window and still accepts verifications
// (the rotation protocol in ADR-125).
type ClusterSigningKey struct {
	ID           int
	KeyID        string
	PublicKeyPEM string
	SealedBlob   []byte
	CreatedAt    time.Time
	RotatedAt    *time.Time
	RetiredAt    *time.Time
}

// LoadClusterSigningKey reads the singleton row. Returns
// ErrNotFound if the table is empty (the pre-bootstrap state).
//
// The query is a straight primary-key SELECT — the singleton
// constraint (CHECK id = 1) makes an index scan unnecessary
// (table has at most one row in steady state).
//
// Callers (cmd/schedd, cmd/gatewayd-internal) translate
// ErrNotFound into "fall back to the FAAS_INTERNAL_SVC_KEY_PATH
// / FAAS_INTERNAL_SVC_PUBKEYS env path" — that fallback is the
// single-box dev + operator-migration path. Production
// multi-host boots require a populated row.
func (s *PgStore) LoadClusterSigningKey(ctx context.Context) (ClusterSigningKey, error) {
	var (
		k         ClusterSigningKey
		rotatedAt *time.Time
		retiredAt *time.Time
	)
	err := s.pool.QueryRow(ctx, `
		SELECT id, key_id, public_key_pem, sealed_blob,
		       created_at, rotated_at, retired_at
		  FROM public.cluster_signing_keys
		 WHERE id = 1
	`).Scan(
		&k.ID, &k.KeyID, &k.PublicKeyPEM, &k.SealedBlob,
		&k.CreatedAt, &rotatedAt, &retiredAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ClusterSigningKey{}, ErrNotFound
		}
		return ClusterSigningKey{}, fmt.Errorf("state: load cluster_signing_keys: %w", err)
	}
	k.RotatedAt = rotatedAt
	k.RetiredAt = retiredAt
	return k, nil
}

// InsertClusterSigningKey writes the singleton row. The
// operator-bootstrap flow (out-of-scope for PR-3; future
// `hostage-gen cluster-init`) is the canonical caller.
//
// ON CONFLICT (id) DO UPDATE supports the rotation protocol:
// the new key replaces the existing row in place. The previous
// key's retired_at is set to now() so verifiers accept both
// kids during the rotation overlap window (load returns the
// row currently in the table; the previous kid is preserved
// only while the row is still present — full rotation-overlap
// semantics require a separate "current + previous" row in a
// follow-on ADR amendment; PR-3 ships the table shape forward-
// compatible but the loader returns one row).
//
// The CHECK constraints on key_id and public_key_pem are
// enforced at INSERT — a malformed kid or PEM fails loud at
// the persist step. SealedBlob is bytea (no length check at
// the table level; pkg/secretbox.SealBytes enforces an upper
// bound at write time).
func (s *PgStore) InsertClusterSigningKey(ctx context.Context, k ClusterSigningKey) error {
	if k.KeyID == "" || k.PublicKeyPEM == "" || len(k.SealedBlob) == 0 {
		return fmt.Errorf("state: insert cluster_signing_keys: empty key_id / public_key_pem / sealed_blob")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO public.cluster_signing_keys
		    (id, key_id, public_key_pem, sealed_blob, created_at, rotated_at, retired_at)
		VALUES (1, $1, $2, $3, now(), NULL, NULL)
		ON CONFLICT (id) DO UPDATE
		    SET key_id         = EXCLUDED.key_id,
		        public_key_pem = EXCLUDED.public_key_pem,
		        sealed_blob    = EXCLUDED.sealed_blob,
		        rotated_at     = COALESCE(public.cluster_signing_keys.rotated_at, now()),
		        retired_at     = CASE
		            WHEN public.cluster_signing_keys.key_id = EXCLUDED.key_id
		                THEN public.cluster_signing_keys.retired_at  -- idempotent re-insert
		                ELSE now()                                     -- rotation: previous kid retired
		        END
	`, k.KeyID, k.PublicKeyPEM, k.SealedBlob)
	if err != nil {
		return fmt.Errorf("state: insert cluster_signing_keys (kid=%s): %w", k.KeyID, err)
	}
	return nil
}

// DeleteClusterSigningKey drops the singleton row. Returns
// ErrNotFound if the table was already empty. Used by tests and
// by the rollback path of the operator-bootstrap CLI.
func (s *PgStore) DeleteClusterSigningKey(ctx context.Context) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM public.cluster_signing_keys WHERE id = 1`)
	if err != nil {
		return fmt.Errorf("state: delete cluster_signing_keys: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
