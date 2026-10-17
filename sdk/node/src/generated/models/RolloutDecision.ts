/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RolloutDecisionSample } from './RolloutDecisionSample.js';
import type { RolloutDecisionSignals } from './RolloutDecisionSignals.js';
/**
 * Evidence behind one decision about a rollout (ADR-1447): why a canary stage advanced, is held, or was aborted. Aggregates only; no request payloads, routes, URLs or customer text.
 */
export type RolloutDecision = {
  kind: 'advance' | 'hold' | 'abort';
  /**
   * Closed set; the breaker rules reuse the canary_progression_circuit_breaker_total event names.
   */
  rule: 'advance_healthy' | 'advance_probe_evidence' | 'advance_low_traffic' | 'advance_no_predecessor' | 'advance_manual' | 'hold_insufficient_samples' | 'hold_signal_unavailable' | 'hold_observation_unavailable' | 'hold_health_alert' | 'abort_5xx' | 'abort_p95_latency' | 'abort_cold_boot_p95' | 'abort_cpu_per_request' | 'abort_dependency_errors' | 'abort_oom' | 'abort_crash_loop' | 'abort_manual' | 'abort_worker_lease_expired' | 'abort_post_release_5xx' | 'abort_post_release_p95_latency';
  /**
   * The circuit breaker's aggregate-only reason.
   */
  summary?: string;
  /**
   * Zero-based canary step that was evaluated.
   */
  step: number;
  /**
   * The candidate's share during the observation window.
   */
  traffic_percent: number;
  window_start?: string;
  window_end?: string;
  stable_deployment_id?: string;
  candidate?: RolloutDecisionSample;
  stable?: RolloutDecisionSample;
  /**
   * The 5xx sample included synthetic probes of the app (ADR-1446).
   */
  probes_included?: boolean;
  signals?: RolloutDecisionSignals;
  decided_at: string;
};

