// cluster_key_verifier_loader.go — gatewayd-internal-side loader for
// the cluster-wide Ed25519 PUBLIC key (PR-3 / audit F1+F20 / ADR-125).
//
// Why this lives in cmd/gatewayd-internal, not pkg/gateway: the
// loader is a thin adapter from *state.PgStore to the
// pkg/gateway.InternalSvcVerifier shape. The verifier bridge in
// cmd/gatewayd-internal/internal_svc_verifier.go stays a thin
// pkg/internalsvc<->pkg/gateway translator (no pgxpool); adding
// the pool import here keeps the bridge untouched and confines
// the SQL to the daemon boundary, matching the
// cmd/schedd/cluster_key_loader.go mirror.
//
// Wire shape (cmd/gatewayd-internal/run.go):
//
//	v, err := loadClusterInternalSvcVerifier(ctx, store)
//	if errors.Is(err, ErrClusterVerifierUnavailable) {
//	    // fall back to FAAS_INTERNAL_SVC_PUBKEYS env (legacy)
//	} else if err != nil {
//	    // hard error — bail
//	}
//	deps.internalSvcVerifier = v
//	go subscribeClusterVerifier(ctx, pool, store, &deps.internalSvcVerifier, log)
//
// Subscribe shape: a long-lived goroutine subscribes to
// db.NotifyClusterSigningKeysChanged and re-runs
// loadClusterInternalSvcVerifier on every delivery. The verifier
// handle is behind an atomic.Pointer so the rotation lands
// without dropping an in-flight request.

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/internalsvc"
	"github.com/onebox-faas/faas/pkg/state"
)

// errEmptyAllowlistVerifier is the rotating verifier's "no keys"
// sentinel. Constructed fresh on each call so errors.Is matches
// by identity (matches the bridge's gatewayEmptyAllowlist
// pattern in cmd/gatewayd-internal/internal_svc_verifier.go).
func errEmptyAllowlistVerifier() error {
	return errors.New(internalsvc.ErrEmptyAllowlist.Error())
}

// ErrClusterVerifierUnavailable is returned by
// loadClusterInternalSvcVerifier when the cluster_signing_keys row
// is missing. Caller translates this into "fall back to
// FAAS_INTERNAL_SVC_PUBKEYS env". A non-nil error of any other
// type is a hard failure (DB unreachable, pgx error, parse error)
// and the caller must surface it — silently falling back to an
// empty allowlist would 500 every internal_only request.
var ErrClusterVerifierUnavailable = errors.New("gatewayd-internal: cluster_signing_keys row missing")

// rotatingVerifier wraps a gateway.InternalSvcVerifier behind an
// atomic.Pointer so the rotation subscriber can swap the active
// verifier without touching every existing call site in the
// gateway. The wrapper itself satisfies gateway.InternalSvcVerifier
// (Verify + AllowedSvcNames) by forwarding to the underlying
// verifier; a nil underlying returns the same "verifier wired
// but no keys" error the bridge would emit, preserving the
// gate's failure semantics during the rotation window.
//
// The atomic.Pointer pattern matches cmd/schedd/internal_svc_minter.go's
// atomicMinter — symmetric load-bearing primitive on both the
// mint and verify sides of the cluster_signing_keys rotation.
type rotatingVerifier struct {
	current atomic.Pointer[gateway.InternalSvcVerifier]
}

// Verify is the hot path. Atomic-load the current verifier and
// delegate. A nil current (initial state, rotation in flight) is
// treated as "verifier wired but no keys" — the gate emits
// reason="empty_allowlist" and the request 500s with a loud log.
// This is deliberately fail-closed: a half-rotation must not
// let an attacker sub the gate.
func (r *rotatingVerifier) Verify(ctx context.Context, rawToken string) (string, error) {
	v := r.current.Load()
	if v == nil || *v == nil {
		return "", errEmptyAllowlistVerifier()
	}
	return (*v).Verify(ctx, rawToken)
}

// AllowedSvcNames forwards to the current verifier. Used by the
// admin endpoint surface (future PR; today tests read it).
func (r *rotatingVerifier) AllowedSvcNames() []string {
	v := r.current.Load()
	if v == nil || *v == nil {
		return nil
	}
	return (*v).AllowedSvcNames()
}

// set is the rotation subscriber's swap primitive. Fail-closed:
// a nil verifier is a logic bug and is refused without affecting
// the current verifier (in-flight requests keep using the
// previous verifier).
func (r *rotatingVerifier) set(v gateway.InternalSvcVerifier) error {
	if v == nil {
		return errors.New("gatewayd-internal: rotatingVerifier.set: nil verifier refused")
	}
	r.current.Store(&v)
	return nil
}

// initial sets the boot-time verifier (used by run.go after
// loadClusterInternalSvcVerifier returns successfully).
func (r *rotatingVerifier) initial(v gateway.InternalSvcVerifier) error {
	return r.set(v)
}

// Compile-time guard: rotatingVerifier satisfies
// gateway.InternalSvcVerifier. If a future contributor changes
// the interface, the build fails here before runtime does.
var _ gateway.InternalSvcVerifier = (*rotatingVerifier)(nil)

// loadClusterInternalSvcVerifier is the PG-side path of the
// gatewayd-internal verifier loader (PR-3). Returns a
// gateway.InternalSvcVerifier whose allowlist is exactly the
// single cluster-wide public key under svc="schedd".
//
// Rotation overlap (accepting retired kids within a grace
// window) is a follow-on ADR-125 amendment — PR-3 ships the
// table shape + the loader, the rotation overlap lands in a
// follow-on PR cluster member. Today's verifier accepts ONE
// key per service; a rotated key on a still-valid retired
// kid is the next iteration's problem.
//
// Returns ErrClusterVerifierUnavailable when the PG row is
// empty. A parse error on public_key_pem is a HARD error
// (unlike the per-host path which silently skips malformed
// entries) — the cluster row is operator-managed and any
// shape mismatch means the cluster key is broken, not
// "just one of many entries happens to be wrong".
func loadClusterInternalSvcVerifier(
	ctx context.Context,
	store *state.PgStore,
) (gateway.InternalSvcVerifier, error) {
	if store == nil {
		return nil, fmt.Errorf("gatewayd-internal: loadClusterInternalSvcVerifier: nil store")
	}
	row, err := store.LoadClusterSigningKey(ctx)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil, ErrClusterVerifierUnavailable
		}
		return nil, fmt.Errorf("gatewayd-internal: load cluster_signing_keys: %w", err)
	}
	if row.PublicKeyPEM == "" {
		return nil, fmt.Errorf("gatewayd-internal: cluster_signing_keys row (kid=%s) has empty public_key_pem", row.KeyID)
	}
	// Construct the allowlist as the singleton { "schedd":
	// row.PublicKeyPEM }. The bridge (newInternalSvcVerifierFromPEMs)
	// parses + caches the public key on construction; rotation
	// means a fresh bridge instance per cluster_signing_keys row.
	return newInternalSvcVerifierFromPEMs(map[string]string{
		"schedd": row.PublicKeyPEM,
	}), nil
}

// SubscribeClusterVerifierChanges is the long-lived rotation
// subscriber for gatewayd-internal (PR-3 / ADR-125). Subscribes
// to db.NotifyClusterSigningKeysChanged and re-runs
// loadClusterInternalSvcVerifier on every delivery, atomic-
// swapping the active verifier via the passed-in
// *rotatingVerifier.
//
// Rotation latency budget: matches schedd's rotation subscriber
// (~5 ms end-to-end on a healthy cluster). In-flight verify
// requests use the previous verifier; the very next request
// after the swap lands uses the new key.
//
// Failure modes mirror the schedd side:
//   - subscribe fails initially (DB unreachable at boot):
//     returns the error; run.go logs + continues with the
//     boot-time verifier.
//   - re-load fails on a delivery: logs a warning and keeps
//     the previous verifier in place. The operator must
//     re-issue the rotation once the unseal path is fixed.
//
// The function returns nil on successful subscribe launch and
// runs the goroutine for the lifetime of ctx — run.go launches
// it in its own goroutine and cancels ctx on shutdown.
func SubscribeClusterVerifierChanges(
	ctx context.Context,
	pool *pgxpool.Pool,
	store *state.PgStore,
	verifier *rotatingVerifier,
	log *slog.Logger,
) error {
	if pool == nil || store == nil || verifier == nil {
		return fmt.Errorf("gatewayd-internal: SubscribeClusterVerifierChanges: nil pool/store/verifier")
	}
	if log == nil {
		log = slog.Default()
	}
	notifs, err := db.SubscribeWithReconnect(ctx, pool,
		[]string{db.NotifyClusterSigningKeysChanged}, log)
	if err != nil {
		return fmt.Errorf("gatewayd-internal: subscribe cluster_signing_keys_changed: %w", err)
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-notifs:
				if !ok {
					log.Warn("gatewayd-internal: cluster_signing_keys_changed channel closed unexpectedly")
					return
				}
				v, vErr := loadClusterInternalSvcVerifier(ctx, store)
				if vErr != nil {
					if errors.Is(vErr, ErrClusterVerifierUnavailable) {
						log.Warn("gatewayd-internal: cluster verifier no longer available on rotation; keeping previous verifier",
							"reason", vErr.Error())
						continue
					}
					log.Warn("gatewayd-internal: cluster verifier rotation re-load failed; keeping previous verifier",
						"err", vErr.Error())
					continue
				}
				if sErr := verifier.set(v); sErr != nil {
					log.Warn("gatewayd-internal: cluster verifier rotation swap refused; keeping previous verifier",
						"err", sErr.Error())
					continue
				}
				log.Info("gatewayd-internal: cluster verifier rotated")
			}
		}
	}()
	return nil
}
