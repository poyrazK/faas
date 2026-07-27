// src/idempotency.ts — request-scoped Idempotency-Key support.
//
// The contract mirrors `sdk/go/idempotency.go` and
// `pkg/api/internal/api/client.go::do`:
//   * Every mutating call (POST/PUT/PATCH/DELETE) MUST carry an
//     `Idempotency-Key` header. The server replays the same response
//     for the same key within 24h (apid's idempotency middleware).
//   * GET/HEAD skip the header — the server doesn't dedupe reads.
//   * Default behaviour: the SDK auto-mints a fresh UUIDv4 on EVERY
//     mutating attempt (per-attempt, not per-call) so the server's
//     24h replay window sees a fresh retry budget per attempt.
//   * Opt-in: callers can pin a stable key via `client.setIdempotencyKey`
//     for retry semantics that survive across processes (CI deploys,
//     idempotent batch jobs, etc.). The key is process-wide, attached
//     to the FaaSClient instance, and reset on the next mutating call
//     unless the caller sets it again.
//
// The async-local-storage wire-in (per-call key without touching
// the FaaSClient instance) is deferred to a doc-driven follow-up;
// today the explicit `setIdempotencyKey` is sufficient.

import { randomUUID } from 'node:crypto';

/** Type alias for an idempotency key. Any non-empty string ≤ 255 chars
 *  is accepted; the server doesn't validate the format. */
export type IdempotencyKey = string;

/**
 * Default key generator. UUIDv4 is canonical because the server's
 * 24h replay window keys on string equality — collisions across the
 * window are vanishingly improbable (UUIDv4 = 122 random bits).
 *
 * Re-exported as a named function so callers can plug in their own
 * (e.g. for tests that need a deterministic key).
 */
export function mintIdempotencyKey(): IdempotencyKey {
  return randomUUID();
}

/**
 * Mutating methods that REQUIRE an Idempotency-Key. Listed explicitly
 * rather than computed from the method because we want a single
 * source of truth for the GET-skip rule.
 */
export const MUTATING_METHODS = new Set([
  'POST',
  'PUT',
  'PATCH',
  'DELETE',
]);

/** Pure predicate: does this HTTP method require an Idempotency-Key? */
export function isMutating(method: string): boolean {
  return MUTATING_METHODS.has(method.toUpperCase());
}