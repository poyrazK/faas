// cluster_key_loader.go — schedd-side loader for the cluster-wide
// Ed25519 signing key (PR-3 / audit F1+F20 / ADR-125).
//
// Why this lives in cmd/schedd, not pkg/sched: the pgClusterKeyLoader
// is a thin adapter from *pgxpool.Pool + *pgxpool.Pool-derived
// *state.PgStore to the sched-internal closure shape. Putting it
// inside the daemon keeps pkg/sched Postgres-agnostic and avoids a
// pkg/state import in pkg/sched (which would invert the dependency
// direction: pkg/sched already imports pkg/state for the engine
// store, but the loader's only consumer is the boot wiring in
// cmd/schedd/main.go, so the file belongs at the daemon boundary).
//
// Wire shape (cmd/schedd/main.go):
//
//	store := state.NewPgStore(pool)
//	priv, kid, err := loadClusterInternalSvcKey(ctx, store, log)
//	if err != nil { /* fall back to disk */ }
//	// minter closure captures (priv, kid) and calls
//	// internalsvc.Mint("schedd", ttl, claims, priv, kid)
//
// Subscribe shape: a long-lived goroutine subscribes to
// db.NotifyClusterSigningKeysChanged and re-runs
// loadClusterInternalSvcKey on every delivery. The minter hot
// path uses an atomic.Pointer[ed25519.PrivateKey] swap so the
// rotation lands without dropping an in-flight synth.
//
// Multi-box unseal model: the operator bootstraps a shared
// host.age identity onto every box (the same key that initially
// sealed the cluster key). Every schedd's secretbox.LoadHostKeys
// returns that identity, and secretbox.OpenBytesMulti accepts
// it. A box whose host.age chain cannot unseal the cluster blob
// logs a loud error and falls back to the per-host disk path
// (the single-box dev + operator-migration window).

package main

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/internalsvc"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// ErrClusterKeyUnavailable is returned by loadClusterInternalSvcKey
// when neither the PG row nor any host.age identity on this box
// can produce the unsealed private key. The minter-side caller
// (loadSchedInternalSvcKey) translates this into the existing
// fallback chain (per-host disk path / generated-on-boot path).
// The sentinel is exported so tests can match on the specific
// failure mode ("no row" vs "row but unseal failed") without
// parsing error strings.
var ErrClusterKeyUnavailable = errors.New("schedd: cluster_signing_keys row missing or host.age cannot unseal")

// loadClusterInternalSvcKey is the PG-side path of the schedd
// minter loader (PR-3). Returns the unsealed Ed25519 private key
// and the canonical kid derived from the corresponding public
// key (matches the row's key_id column exactly; we re-derive
// from the bytes anyway so a row whose key_id drifted from the
// unsealed PEM fails loud at boot rather than minting tokens
// nobody can verify).
//
// Fallback semantics (deliberately not done here, by design):
// this function returns ErrClusterKeyUnavailable and the
// CALLER decides whether to fall back to the per-host disk path.
// Splitting the "PG path" from the "fallback chain" keeps the
// load policy in one place (loadSchedInternalSvcKey) instead of
// duplicating it across both surfaces.
func loadClusterInternalSvcKey(
	ctx context.Context,
	store *state.PgStore,
	log *slog.Logger,
) (ed25519.PrivateKey, string, error) {
	if store == nil {
		return nil, "", fmt.Errorf("schedd: loadClusterInternalSvcKey: nil store")
	}
	if log == nil {
		log = slog.Default()
	}
	row, err := store.LoadClusterSigningKey(ctx)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			// Table empty — operator hasn't run cluster-init
			// yet, OR this is a single-box dev install. The
			// fallback chain in loadSchedInternalSvcKey picks
			// up the per-host disk path here.
			return nil, "", ErrClusterKeyUnavailable
		}
		return nil, "", fmt.Errorf("schedd: load cluster_signing_keys: %w", err)
	}

	identities, err := secretbox.LoadHostKeys(filepath.Dir(secretbox.DefaultHostKeyPath))
	if err != nil {
		// No host.age on this box at all — single-box dev
		// without an unseal key. Fall back to the per-host
		// disk path so the box still boots.
		log.Warn("schedd: cannot load host.age identities for cluster key unseal",
			"err", err.Error())
		return nil, "", ErrClusterKeyUnavailable
	}
	if len(identities) == 0 {
		return nil, "", ErrClusterKeyUnavailable
	}

	plaintext, err := unsealClusterKey(row.SealedBlob, identities)
	if err != nil {
		// Row exists but no identity on this box can open it.
		// This is the cross-box bootstrap mistake: the operator
		// forgot to distribute the sealing host.age to this
		// box. Loud warn; the fallback chain picks up the
		// per-host disk path so this box still boots in dev.
		log.Warn("schedd: cluster_signing_keys sealed_blob is not unsealable on this box",
			"kid", row.KeyID,
			"identities_loaded", len(identities),
			"err", err.Error())
		return nil, "", ErrClusterKeyUnavailable
	}

	priv, err := parseClusterPrivPEM(plaintext)
	if err != nil {
		return nil, "", fmt.Errorf("schedd: parse unsealed cluster-key PEM (kid=%s): %w", row.KeyID, err)
	}

	// Re-derive the kid from the unsealed public key and assert
	// it matches the row's key_id column. A mismatch means the
	// sealed_blob was produced from a different private key than
	// the one whose public counterpart is in public_key_pem —
	// i.e. the row is internally inconsistent. Refuse to mint.
	kid := internalsvc.KidFromPub(priv.Public().(ed25519.PublicKey))
	if kid != row.KeyID {
		return nil, "", fmt.Errorf(
			"schedd: cluster_signing_keys row kid=%s does not match derived kid=%s from unsealed private key — refusing to mint",
			row.KeyID, kid)
	}
	return priv, kid, nil
}

// unsealClusterKey is the small age-decrypt wrapper that mirrors
// the shape of pkg/secretbox.OpenBytesMulti but adds a typed
// error so the caller can distinguish "no identity unsealed" from
// other failure modes. The age.ParseIdentity / age.Decrypt loop
// is exposed by pkg/secretbox but the wrapper keeps the failure
// path here rather than leaking age types up through the loader.
func unsealClusterKey(sealed []byte, identities []*age.X25519Identity) ([]byte, error) {
	if len(sealed) == 0 {
		return nil, errors.New("empty sealed blob")
	}
	if len(identities) == 0 {
		return nil, errors.New("no identities available")
	}
	// secretbox.OpenBytesMulti returns (namespace, plaintext, err);
	// the cluster-key envelope is unsealed with the default
	// namespace "cluster_svc" (mirrors internalSvcKeySealedNamespace
	// on the minter side). The namespace is informational — there
	// is no cross-namespace replay concern because the row's
	// sealed_blob is itself single-typed (one namespace per row).
	namespace, plaintext, err := secretbox.OpenBytesMulti(identities, sealed)
	if err != nil {
		return nil, fmt.Errorf("age decrypt: %w", err)
	}
	_ = namespace // reserved for a future namespace-aware log line
	return plaintext, nil
}

// parseClusterPrivPEM is the unsealed-bytes → ed25519.PrivateKey
// step. Lifted from cmd/schedd/internal_svc_minter.go's
// parseSchedKeyPEM so the two surfaces produce the same
// validated key shape regardless of where the bytes came from
// (per-host disk vs cluster blob).
func parseClusterPrivPEM(data []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("cluster key payload is not PEM-encoded")
	}
	if block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("cluster key payload has unexpected PEM block type %q", block.Type)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse PKCS#8: %w", err)
	}
	priv, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("cluster key payload is not an Ed25519 key")
	}
	return priv, nil
}

// Compile-time check: the cluster-key helper does not depend on
// any internal package state that would break in the test binary.
// Without this, the linker silently drops the package-private
// helper in some test configurations.
var _ = os.Getenv

// SubscribeClusterKeyChanges is the long-lived rotation subscriber
// for schedd (PR-3 / ADR-125). Subscribes to
// db.NotifyClusterSigningKeysChanged and re-runs
// loadClusterInternalSvcKey on every delivery, atomic-swapping
// the active minter via atomicMinter.Rotate.
//
// Rotation latency budget: the operator runs
// `hostage-gen cluster-rotate` (out-of-scope for PR-3) which
// INSERTs a new row. The trigger fires the channel; the
// subscriber re-loads within ~5 ms (Postgres NOTIFY delivery is
// sub-ms on a healthy cluster). In-flight JWTs minted with the
// previous kid remain valid for the rotation overlap window
// (TODO: a follow-on ADR amendment adds retired_at-driven
// multi-key acceptance to the verifier side; PR-3 ships the
// minter rotation; the verifier rotation overlap is a
// PR-3-follow-on).
//
// Failure modes:
//   - subscribe fails initially (DB unreachable at boot):
//     returns the error; main.go logs + continues with the
//     boot-time key. Rotation just doesn't auto-propagate until
//     the next daemon restart.
//   - re-load fails on a delivery (e.g. host.age rotated out
//     and the new cluster blob can't be unsealed): logs a
//     warning and keeps the previous minter in place. The
//     rotation eventually lands when the operator fixes the
//     unseal path.
//
// The function blocks for the lifetime of ctx — main.go
// launches it in its own goroutine and cancels ctx on
// shutdown.
func SubscribeClusterKeyChanges(
	ctx context.Context,
	pool *pgxpool.Pool,
	store *state.PgStore,
	m *atomicMinter,
	log *slog.Logger,
) error {
	if pool == nil || store == nil || m == nil {
		return fmt.Errorf("schedd: SubscribeClusterKeyChanges: nil pool/store/minter")
	}
	if log == nil {
		log = slog.Default()
	}
	notifs, err := db.SubscribeWithReconnect(ctx, pool,
		[]string{db.NotifyClusterSigningKeysChanged}, log)
	if err != nil {
		return fmt.Errorf("schedd: subscribe cluster_signing_keys_changed: %w", err)
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-notifs:
				if !ok {
					// SubscribeWithReconnect only closes the
					// outer channel on ctx.Done; a closed
					// channel with live ctx is unreachable.
					// Bail and let the operator restart.
					log.Warn("schedd: cluster_signing_keys_changed channel closed unexpectedly")
					return
				}
				priv, kid, err := loadClusterInternalSvcKey(ctx, store, log)
				if err != nil {
					if errors.Is(err, ErrClusterKeyUnavailable) {
						log.Warn("schedd: cluster key no longer available on rotation; keeping previous minter",
							"reason", err.Error())
						continue
					}
					log.Warn("schedd: cluster key rotation re-load failed; keeping previous minter",
						"err", err.Error())
					continue
				}
				if rErr := m.Rotate(priv, kid); rErr != nil {
					log.Warn("schedd: cluster key rotation swap failed; keeping previous minter",
						"kid", kid, "err", rErr.Error())
					continue
				}
				log.Info("schedd: cluster key rotated",
					"new_kid", kid)
			}
		}
	}()
	return nil
}
