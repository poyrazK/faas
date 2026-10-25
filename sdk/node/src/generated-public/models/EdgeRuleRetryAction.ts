/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Request replay against a different healthy instance
 * (ADR-201 §1, kind=retry).
 *
 * There is deliberately NO "retry on status" field. Only a
 * TRANSPORT failure may arm a replay — a guest that answered 5xx
 * has served the request, and replaying it would run the
 * customer's side effects a second time — so the set of
 * retryable conditions is not configurable.
 *
 * A replay additionally requires that the response has not
 * committed, a healthy sibling exists, the aggregate per-app
 * retry budget has capacity, and the remaining `kind=budget`
 * allowance is at least `min_remaining_ms`. A retry can never
 * extend a request's deadline.
 *
 */
export type EdgeRuleRetryAction = {
  /**
   * Total attempts, NOT retries: 2 means the original plus one
   * replay. 0 applies the default (2). 1 is rejected — it is a
   * silent no-op that reads like protection.
   *
   * The ceiling is deliberately tight: every replay holds
   * another instance's concurrency slot for the duration of the
   * request, so a high attempt count multiplies load against
   * your own capacity at exactly the moment instances are
   * already failing.
   *
   */
  max_attempts?: number;
  /**
   * Opt POST and PATCH into replay. Off by default. Even when
   * enabled, these methods are replayed only when the request
   * carries a non-empty `Idempotency-Key` header.
   *
   * This is the only field here that can cost correctness
   * rather than latency: your handler must honor the key and
   * return the stored result instead of repeating the side
   * effect. GET, HEAD, OPTIONS, TRACE, PUT and DELETE are
   * replayed without this flag.
   *
   */
  allow_non_idempotent?: boolean;
  /**
   * Request-budget floor below which a replay is skipped. 0
   * applies the default (250ms). Below this floor a retry
   * mostly converts a 502 into a 504 without improving the
   * outcome.
   *
   */
  min_remaining_ms?: number;
  /**
   * Delay before a replay. Defaults to 0 because the failure
   * being retried is a dead peer, not a loaded one, and the
   * next instance is a different process — so waiting buys
   * nothing. Raise it only when the sibling may still be
   * waking.
   *
   */
  backoff_ms?: number;
  /**
   * Aggregate replay allowance as a percentage of original
   * requests in the gateway's short per-app window. For
   * example, 10 permits at most one replay per ten originals,
   * preventing a broad outage from doubling all traffic.
   *
   */
  budget_percent?: number;
  /**
   * Minimum replay allowance per app and accounting window.
   * The default preserves one recovery opportunity for a
   * low-traffic app even when budget_percent rounds down.
   *
   */
  budget_min_retries?: number;
};

