/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Standalone per-route body-size cap (ADR-091 D24 / §4.1.2.13).
 * The primitive for "POST /upload ≤ 5 MB, POST /users ≤ 1 MB,
 * POST /webhooks ≤ 2 MB" without shipping a JSON Schema. The
 * hot-path applier (§4.1.2.8c) installs `http.MaxBytesReader`
 * on the inbound body and short-circuits oversize requests
 * with 413 `request_too_large` — and, more importantly,
 * performs a Content-Length fast-path deny so a 30 MB body on
 * a 5 MB cap costs zero bytes of buffering (a `MaxBytesReader`
 * alone only trips when something reads the body, and on this
 * hot path nothing reads it until the proxy leg).
 *
 * Rejections never reach the wake gate, the auth chain, or the
 * rate limiter — same posture as kind=validate. Free-and-above
 * (no plan gate).
 *
 * Field-by-field:
 * * `max_body_bytes` — required buffered-path cap. Must be
 * > 0 and ≤ `MaxRequestBodyBytes` (25 MiB). A standalone
 * limit rule with no cap is a silent no-op and is
 * rejected at create-time with 422 — use `kind=validate`
 * if you need a body cap alongside a JSON Schema.
 * * `max_body_bytes_streaming` — optional streaming opt-in
 * cap (≤ `MaxEdgeRuleLimitBodyBytesStreaming` = 100 MiB).
 * 0 (default) = no streaming carve-out; the buffered
 * `max_body_bytes` is the cap on both paths. When set,
 * must be ≥ `max_body_bytes` — a streaming cap tighter
 * than the buffered cap would 413 every streaming request
 * for a body already accepted as buffered. Runtime
 * enforcement of this field is declared + clamped at
 * create-time but deferred at the §4.1.2.8c slot to a
 * follow-up PR (stated in ADR-091 D24 §6).
 *
 */
export type EdgeRuleLimitAction = {
  /**
   * Required per-rule buffered-path body cap. Must be > 0
   * and ≤ `api.MaxRequestBodyBytes` (25 MiB). A standalone
   * limit rule with no cap is rejected at create-time with
   * 422 — use `kind=validate` if you need a body cap
   * alongside a JSON Schema.
   *
   */
  max_body_bytes: number;
  /**
   * Optional streaming opt-in cap. 0 (default) = no
   * streaming carve-out; the buffered `max_body_bytes` is
   * the cap on both paths. Must be ≥ `max_body_bytes` when
   * set. Runtime enforcement deferred to follow-up PR
   * (ADR-091 D24 §6).
   *
   */
  max_body_bytes_streaming?: number;
};

