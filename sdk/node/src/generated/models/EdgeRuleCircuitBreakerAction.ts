/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Instance health thresholds (ADR-201 §2, kind=circuit_breaker).
 *
 * This rule TUNES a breaker that already runs for every app on
 * every plan; it does not enable protection. With no rule your
 * app is still protected from a flapping instance by the
 * platform defaults below.
 *
 * States are closed → open → half_open. In half_open exactly one
 * request is admitted as a probe: success closes the circuit and
 * resets the backoff, failure returns it to open with the
 * interval doubled, up to `max_open_seconds`.
 *
 */
export type EdgeRuleCircuitBreakerAction = {
  /**
   * Failure RATIO (not a count) at or above which a closed
   * breaker opens. Consulted only once `min_requests`
   * observations exist inside the window. 0 applies the
   * default (0.5).
   *
   */
  failure_threshold?: number;
  /**
   * Minimum observations inside `window_seconds` before the
   * ratio is consulted at all. 0 applies the default (5).
   *
   * This is the field most likely to be set badly. At 1 a
   * single transport blip opens the circuit, which on a route
   * serving one request a minute reads as a 100% failure rate.
   *
   */
  min_requests?: number;
  /**
   * Rolling failure window. 0 applies the default (10s).
   *
   */
  window_seconds?: number;
  /**
   * First open interval before a half-open probe is offered. 0
   * applies the default (5s). Each failed probe doubles the
   * interval up to `max_open_seconds`.
   *
   */
  open_seconds?: number;
  /**
   * Ceiling the exponential backoff grows toward. 0 applies the
   * default (60s). Must be at least `open_seconds`. The cap is
   * 1 hour: a longer bench outlives most instances, so the
   * breaker would be holding state about a target that no
   * longer exists.
   *
   */
  max_open_seconds?: number;
};

