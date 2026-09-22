/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-upstream egress circuit-breaker policy (ADR-201 §3).
 *
 * When enabled, your app's NEW connections to this upstream are
 * rejected with a TCP reset while the circuit is open, instead of
 * hanging for a full TCP connect timeout. That is the point of the
 * feature: a blackholed dependency otherwise burns the per-request
 * budget and then holds the wake slot on every request, turning one
 * dependency outage into an app-wide capacity outage. Connections
 * already established are never torn down.
 *
 * The circuit is driven by the platform's own 30-second TCP+TLS
 * probe of the upstream, not by your traffic, so the half-open trial
 * that decides recovery costs your app nothing.
 *
 * Disabled by default. This rule can cut an app off from its own
 * database, so it is never inferred — you opt in per upstream.
 *
 */
export type EgressCircuitBreakerPolicy = {
  /**
   * Whether egress breaking is active for this upstream.
   */
  enabled: boolean;
  /**
   * Failure RATIO (not a count) at or above which the circuit
   * opens. Omitted tracks the platform default (0.5), so the
   * upstream follows the default as it evolves rather than
   * freezing today's value.
   *
   */
  failure_threshold?: number;
  /**
   * Probe samples required inside the window before the ratio is
   * consulted. Omitted tracks the platform default (3). The probe
   * samples every 30s, so a high value can take a long time to
   * accumulate — or never will on a short-lived app.
   *
   */
  min_samples?: number;
  /**
   * First open interval before a half-open probe is offered.
   * Omitted tracks the platform default (30). Each failed probe
   * doubles the interval, up to the platform ceiling.
   *
   */
  open_seconds?: number;
  /**
   * Observed circuit state. Absent when the breaker is disabled or
   * the scheduler has not yet reported one.
   *
   */
  readonly state?: 'closed' | 'open' | 'half_open';
};

