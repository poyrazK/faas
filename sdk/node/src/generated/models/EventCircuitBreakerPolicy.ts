/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Opt-in routing circuit breaker. Omitted properties use defaults. Failures and successful admissions form the sample; capacity waits and filtered events are excluded. Retained history must cover the observation window.
 */
export type EventCircuitBreakerPolicy = {
  failure_threshold_pct?: number;
  min_samples?: number;
  window_seconds?: number;
  cooldown_seconds?: number;
  probe_successes?: number;
  recovery_max_rate_per_second?: number;
  recovery_seconds?: number;
};

