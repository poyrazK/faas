// src/errors.ts — RFC 7807 error surface for the Node SDK.
//
// The four sentinels below mirror the Go SDK's `faas.Err*` family
// (sdk/go/errors.go:1-50). They are the contract: `err instanceof
// ErrNotFound` is the Node equivalent of `errors.Is(err, faas.ErrNotFound)`.
// Each sentinel carries the parsed `Problem` envelope, the HTTP status,
// and the daemon's `tx_id` (for support tickets). Customers who need
// other envelope fields can read `err.problem` directly.
//
// The 1:1 mapping between Problem.code values and sentinels is the
// canonical contract (api/openapi.yaml::components.schemas.Problem.code
// enum — `not_found`, `unauthorized`, `rate_limited`, `capacity_unavailable`).
// Adding a new sentinel requires a new code enum value + an ADR.

/**
 * Problem wire shape — RFC 7807 with platform extensions
 * (`limit`, `observed`, `docs_url`, `checkout_url`,
 * `billing_portal_url`, `paddle_checkout_url`, `tx_id`). Mirrors
 * `sdk/go/internal/api/errors.go::Problem` and
 * `pkg/api/errors.go::Problem` byte-for-byte on field names.
 *
 * Optional fields are marked `?`; consumers must guard each access
 * (the Node SDK sets `noUncheckedIndexedAccess:true` in tsconfig).
 */
export interface Problem {
  type?: string;
  title?: string;
  status?: number;
  /** Canonical machine code (e.g. `not_found`). Mirrors the
   *  `code` enum in api/openapi.yaml:components.schemas.Problem. */
  code?: string;
  detail?: string;
  /** Daemon-assigned request ID. Customers can quote this in
   *  support tickets; it appears in apid logs verbatim. */
  tx_id?: string;
  /** Limit value (quota errors). Numeric; clients should render with
   *  locale-aware formatting. */
  limit?: number;
  /** Observed value (quota errors). */
  observed?: number;
  /** Docs URL pointing at the error code's reference page. */
  docs_url?: string;
  /** Provider-neutral hosted checkout URL (billing errors). */
  checkout_url?: string;
  /** Provider billing portal URL (billing errors). */
  billing_portal_url?: string;
  /** Legacy Paddle checkout URL (plan-upgrade errors). */
  paddle_checkout_url?: string;
}

/**
 * FaasError is the abstract base for every SDK-thrown error that
 * carries a Problem envelope. Customers should test with
 * `isFaasError(err)` before reading `err.problem` / `err.status` /
 * `err.txId`. The four concrete sentinels below are the canonical
 * error codes customers should match against.
 */
export interface FaasError extends Error {
  readonly problem: Problem;
  readonly status: number;
  readonly txId?: string;
}

/**
 * Internal constructor used by the wrapper after the daemon returns a
 * Problem-shaped body. Customers should not call this directly; the
 * sentinels below cover the four canonical cases.
 */
class FaasErrorImpl extends Error implements FaasError {
  readonly problem: Problem;
  readonly status: number;
  readonly txId?: string;
  constructor(problem: Problem, status: number) {
    // The `message` is preserved for legacy `err.message` readers;
    // `toString()` overrides the default `Error: <msg>` shape so
    // log lines carry the canonical code (mirrors Go's
    // `APIError.Error()` which returns `problem.detail`).
    super(`[${status}] ${problem.code ?? 'unknown'}: ${problem.detail ?? problem.title ?? ''}`.trim());
    this.name = new.target.name;
    this.problem = problem;
    this.status = status;
    this.txId = problem.tx_id;
  }
  /**
   * Structured string form used by `slog`-style loggers. Format:
   *   `ErrNotFound [404 not_found]: app missing-app-404 not found`
   * The optional trailing `tx=` block helps support-ticket workflows
   * match the daemon's audit log line. Falls back to the parent's
   * toString when the problem is empty.
   */
  override toString(): string {
    const code = this.problem.code ?? 'unknown';
    const detail = this.problem.detail ?? this.problem.title ?? '';
    const tx = this.txId ? ` tx=${this.txId}` : '';
    return detail
      ? `${this.name} [${this.status} ${code}]: ${detail}${tx}`
      : `${this.name} [${this.status} ${code}]${tx}`;
  }
}

/** `code: "not_found"` — the requested resource does not exist. */
export class ErrNotFound extends FaasErrorImpl {
  constructor(problem: Problem, status = 404) {
    super(problem, status);
  }
}

/** `code: "unauthorized"` — missing/invalid credentials or scope. */
export class ErrUnauthorized extends FaasErrorImpl {
  constructor(problem: Problem, status = 401) {
    super(problem, status);
  }
}

/** `code: "rate_limited"` — request was rate-limited. The retry
 *  RoundTripper handles 429s automatically when configured; this
 *  sentinel surfaces 429s the retry budget didn't catch (e.g. user
 *  set `retry.maxAttempts=0`). */
export class ErrRateLimited extends FaasErrorImpl {
  constructor(problem: Problem, status = 429) {
    super(problem, status);
  }
}

/** `code: "capacity_unavailable"` — plan/region capacity exhausted;
 *  the customer should upgrade or wait. Surfaces
 *  `billing_portal_url` / `paddle_checkout_url` for one-click upgrade. */
export class ErrCapacity extends FaasErrorImpl {
  constructor(problem: Problem, status = 503) {
    super(problem, status);
  }
}

/**
 * Map a Problem.code string to its sentinel constructor.
 * Returns `null` for codes outside the canonical four — the wrapper
 * raises the closest-by-status sentinel (404→ErrNotFound,
 * 401→ErrUnauthorized, 429→ErrRateLimited, 503→ErrCapacity) when the
 * code is unknown so customers still get a typed error. Codes we have
 * no contract for fall through to `ErrUnauthorized` as the safest
 * default.
 */
export function problemToError(problem: Problem, status: number): FaasError {
  switch (problem.code) {
    case 'not_found':
      return new ErrNotFound(problem, status);
    case 'unauthorized':
      return new ErrUnauthorized(problem, status);
    case 'rate_limited':
      return new ErrRateLimited(problem, status);
    case 'capacity_unavailable':
      return new ErrCapacity(problem, status);
    default:
      // Fall back to status-based mapping for forward-compat with new
      // codes that ship after this SDK release.
      if (status === 404) return new ErrNotFound(problem, status);
      if (status === 401) return new ErrUnauthorized(problem, status);
      if (status === 429) return new ErrRateLimited(problem, status);
      if (status === 503) return new ErrCapacity(problem, status);
      return new ErrUnauthorized(problem, status);
  }
}

/** Type guard: `err instanceof FaasError` plus a non-null
 *  `problem` field. Use this before reading `err.problem.status` etc.
 *  — it narrows the type for the rest of the block. */
export function isFaasError(err: unknown): err is FaasError {
  return err instanceof FaasErrorImpl;
}

/** Convenience: equivalent to `isFaasError(err) ? err : null`.
 *  Mirrors Go's `errors.As(err, &apiErr)`. */
export function asFaasError(err: unknown): FaasError | null {
  return isFaasError(err) ? err : null;
}
