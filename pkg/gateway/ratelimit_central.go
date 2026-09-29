// Package gateway — central-mode rate-limit backend seam (ADR-104
// amendment 5, issue #881 Phase 4).
//
// The pre-Phase-4 Limiter (pkg/gateway/ratelimit.go) is in-process:
// every gatewayd-internal replica owns a private bucket map and the
// 00126 pg_ratelimit_counters table exists but has zero Go readers.
// On a multi-replica Tier-A7 cluster, sticky-by-warm-node routing
// (ADR-070) does NOT pin a single replica, so per-process buckets
// see a fraction of customer traffic and the limit leaks.
//
// CentralBackend is the seam that closes the leak:
//
//   1. Every request atomically consumes from Postgres, making one shared
//      burst authoritative across all gateway replicas.
//   2. The in-process limiter mirrors the returned balance for response
//      headers and is used only for response headers; a central-store error rejects admission.
//   3. The local mirror is overwritten with the authoritative remaining
//      balance after each successful consume.
//
// Phase 4 covers per-app + per-account + per-rule scopes (the 00281
// migration widened the 00126 CHECK to include scope='rule'). ADR-104
// amendment 6 also coordinates dimensional rule buckets without widening
// the table: a bounded deterministic shard is encoded as a derived UUID in
// subject_id, so the existing (scope, subject_id, plan) key remains valid.

package gateway

import "context"

// CentralBackend is the production-side seam that allows a Limiter
// to coordinate across N gatewayd-internal replicas via a shared
// Postgres counter row (pg_ratelimit_counters, migration 00126,
// widened by migration 00281 to include scope='rule').
//
// Daemon startup wires state.PGRateLimitBackend by default (ADR-375).
// Explicit local mode and library constructors use noopCentralBackend.
//
// ConsumeToken / PeekToken signatures use the same (scope, subject_id,
// plan) key triple as the central SQL row; cost is always 1 token per
// Allow call (the limiter math is fixed). Future per-request-cost
// variations (e.g. byte-weighted limits) would extend the signature
// without breaking the closed set of existing call sites.
type CentralBackend interface {
	// ConsumeToken attempts to consume one token from the
	// central counter for (scope, subject_id, plan). Returns:
	//
	//   remaining int  — tokens remaining AFTER the consume
	//                    (>= 0; the consume always succeeds or
	//                    never happened — there is no negative
	//                    balance on the wire)
	//   ok bool        — true iff the consume succeeded (i.e.,
	//                    remaining tokens >= 0 after refill + -1)
	//   err error      — non-nil iff Postgres was unreachable or
	//                    the atomic counter operation failed; the caller
	//                    MUST reject unverified shared admission (ADR-375)
	//
	// A single upsert serialises contending replicas with the counter row lock.
	ConsumeToken(ctx context.Context, scope, subjectID, plan string, rps, burst float64) (remaining int, ok bool, err error)

	// PeekToken returns the central counter's current tokens without
	// decrementing. It is retained for diagnostics and compatibility; request
	// admission uses the atomic ConsumeToken operation.
	//
	// Returns:
	//   remaining int  — tokens currently available centrally
	//                    (>= 0; the row's tokens column)
	//   err error      — non-nil iff Postgres was unreachable;
	//                    the diagnostic result is unavailable.
	PeekToken(ctx context.Context, scope, subjectID, plan string) (remaining int, err error)

	// Invalidate drops any implementation-specific cache entry for
	// (scope, subject_id, plan). The production Postgres backend has no cache;
	// this method remains for compatibility and operator-triggered resets.
	Invalidate(scope, subjectID, plan string)
}

// CentralFailureBackend is optional because response-driven counters need a
// check before compute and a separate, debt-preserving record after an app
// failure. Request token backends need not implement this extension.
type CentralFailureBackend interface {
	CheckPreAuthFailure(ctx context.Context, subjectID, plan string, rps float64, burst int) (allowed bool, retryAfter int, err error)
	RecordPreAuthFailure(ctx context.Context, subjectID, plan string, rps float64, burst int) error
}

// noopCentralBackend identifies an explicitly local library limiter. The
// daemon must select local mode deliberately; a central error never selects it.
type noopCentralBackend struct{}

// Compile-time interface check.
var _ CentralBackend = noopCentralBackend{}

// ConsumeToken is unused for local admission; the local bucket owns that mode.
func (noopCentralBackend) ConsumeToken(context.Context, string, string, string, float64, float64) (int, bool, error) {
	return 0, true, nil
}

// PeekToken on a noop backend returns (0, nil). Admission bypasses this method.
func (noopCentralBackend) PeekToken(context.Context, string, string, string) (int, error) {
	return 0, nil
}

// Invalidate on a noop backend is a no-op — there is no in-process
// cache for the central counter to invalidate.
func (noopCentralBackend) Invalidate(string, string, string) {}
