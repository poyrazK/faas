// pgstore_ratelimit.go — Postgres-backed CentralBackend for
// pg_ratelimit_counters (ADR-104 amendment 5, issue #881 Phase 4 C3).
//
// This file is the production-side implementation of the
// CentralBackend interface declared in pkg/gateway. It lives
// in pkg/state (alongside pgstore.go + the sqlc-generated queries)
// because:
//
//   1. The Store adapter already owns the pgxpool — no need to
//      open a second pool just for the counter.
//   2. The atomic consume uses sqlc (ADR-570). Existing diagnostic and
//      maintenance queries retain the ADR-041 single-statement carve-out.
//
// Interface assertion: the compile-time `var _ gateway.CentralBackend =
// (*PGRateLimitBackend)(nil)` lives in
// cmd/gatewayd-internal/run.go's wireup_test.go because pkg/state
// does not import pkg/gateway (and adding the import would
// invert the package layering — cf. memory
// pkg-api-cannot-import-pkg-state.md).
//
// # SQL shape
//
//	INSERT INTO pg_ratelimit_counters (scope, subject_id, plan, tokens, last_refill)
//	VALUES ($1, $2, $3, $4, now())
//	ON CONFLICT (scope, subject_id, plan) DO UPDATE
//	  SET tokens = LEAST(burst, tokens + FLOOR(elapsed * rps)) - 1,
//	      last_refill = last_refill + consumed_refill_time
//	  WHERE LEAST(burst, tokens + FLOOR(elapsed * rps)) >= 1
//	RETURNING tokens;
//
// ON CONFLICT's row lock serialises replicas in one statement and one round
// trip. Advancing last_refill only by whole-token refill time preserves the
// fractional elapsed remainder; setting it to now on every request would
// prevent a drained bucket from refilling under steady traffic.
//
// # Store outage posture
//
// A store error refuses unverified admission (ADR-570). No process-local
// allowance is substituted; the gateway returns rate_limit_unavailable/503.
//
// # Dimensional rule scope
//
// The backend remains intentionally unaware of consumer identities. The
// gateway maps each dimensional rule value into one of a bounded number of
// deterministic UUID subjects (ADR-104 amendment 6), allowing replicas to
// share counters without widening this table or persisting raw claims.

package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RateLimitRow is the minimum row projection PGRateLimitBackend
// reads from pg_ratelimit_counters. Tokens are bigint today; a
// future ADR that adds fractional refill (cf. 00126 comment
// lines 28-34) would change the column type to numeric(20,4).
type RateLimitRow struct {
	Scope      string
	SubjectID  string
	Plan       string
	Tokens     int64
	LastRefill time.Time
}

// PGRateLimitBackend is the production CentralBackend. Constructed
// by cmd/gatewayd-internal/run.go iff [ratelimit] mode = "central"
// (the TOML knob added in C2). The pool is shared with the rest
// of the daemon (no second pool).
type PGRateLimitBackend struct {
	pool *pgxpool.Pool
}

// NewPGRateLimitBackend wires the production backend. The caller supplies the
// resolved refill policy on each consume so app, account, and route limits can
// share the same storage implementation.
func NewPGRateLimitBackend(pool *pgxpool.Pool) *PGRateLimitBackend {
	return &PGRateLimitBackend{pool: pool}
}

// ConsumeToken attempts to consume one token from the central
// counter for (scope, subjectID, plan). Implements
// gateway.CentralBackend (the interface assertion lives in
// run.go's wireup_test.go).
//
// Returns:
//
//	remaining int   — tokens remaining AFTER the consume (>= 0).
//	                  A return of (0, false, nil) signals the
//	                  bucket is empty and the caller must reject.
//	ok bool         — true iff the consume succeeded.
//	err error       — non-nil iff Postgres was unreachable or
//	                  the advisory lock deadlocked; the caller
//	                  MUST reject unverified shared admission.
func (b *PGRateLimitBackend) ConsumeToken(ctx context.Context, scope, subjectID, plan string, rps, burst float64) (int, bool, error) {
	if rps <= 0 || burst < 1 {
		return 0, false, nil
	}
	id, err := uuid.Parse(subjectID)
	if err != nil {
		return 0, false, fmt.Errorf("rate-limit subject: %w", err)
	}
	remaining, err := sqlc.New().ConsumeTrafficRateToken(ctx, b.pool, sqlc.ConsumeTrafficRateTokenParams{
		Scope: scope, SubjectID: pgtype.UUID{Bytes: id, Valid: true}, Plan: plan, Burst: burst, Rps: rps,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("ratelimit central ConsumeToken: %w", err)
	}
	return int(remaining), true, nil
}

// PeekToken returns the central counter's current tokens WITHOUT
// decrementing. Implements gateway.CentralBackend.
//
// Returns:
//
//	remaining int   — tokens currently available centrally.
//	err error       — non-nil iff Postgres was unreachable.
func (b *PGRateLimitBackend) PeekToken(ctx context.Context, scope, subjectID, plan string) (int, error) {
	const q = `
		SELECT tokens FROM pg_ratelimit_counters
		 WHERE scope = $1 AND subject_id = $2 AND plan = $3`
	var remaining int64
	if err := b.pool.QueryRow(ctx, q, scope, subjectID, plan).Scan(&remaining); err != nil {
		// pgx returns pgx.ErrNoRows for a missing key; treat
		// as zero (the consume path will INSERT-on-conflict
		// the row on the next Allow).
		return 0, fmt.Errorf("ratelimit central PeekToken: %w", err)
	}
	return int(remaining), nil
}

// Invalidate drops any in-process cache entry for (scope, subjectID, plan).
// Implements gateway.CentralBackend and is retained for compatibility with
// explicit operator resets.
//
// The PGRateLimitBackend itself has no in-process cache to
// invalidate — Postgres IS the shared state — so this is a
// no-op. The signature is here to satisfy the interface.
func (b *PGRateLimitBackend) Invalidate(scope, subjectID, plan string) {}

// PrunePreAuthCounters removes one bounded batch of idle shared source
// counters. Pre-auth routes refill at at least one token per second and have
// burst at most 2,000; after two hours without a successful consume every
// bucket is fully refilled, so deletion cannot forgive outstanding rate debt.
// Other central scopes are deliberately untouched.
func (b *PGRateLimitBackend) PrunePreAuthCounters(ctx context.Context) (int64, error) {
	const q = `
		WITH stale AS (
			SELECT ctid FROM pg_ratelimit_counters
			WHERE scope = 'preauth' AND last_refill < now() - interval '2 hours'
			ORDER BY last_refill
			LIMIT 1000 FOR UPDATE SKIP LOCKED
		)
		DELETE FROM pg_ratelimit_counters
		WHERE ctid IN (SELECT ctid FROM stale)`
	result, err := b.pool.Exec(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("prune pre-auth rate-limit counters: %w", err)
	}
	return result.RowsAffected(), nil
}
