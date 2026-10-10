/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The classified dependency most likely behind this route regression (ADR-934): the one whose p95 regressed most between the previous and the regressed deployment, else one that started failing more often. Bounded and redacted; also sent in debug.regression.* webhooks.
 */
export type DebugSuspectedDependency = {
  type: 'managed_binding' | 'outbound_integration' | 'guest_transport' | 'platform_internal' | 'app_dependency';
  kind?: string;
  name: string;
  p95_base_ms: number;
  p95_ms: number;
  regression_factor: number;
  /**
   * latency when the dependency's p95 regressed; failures when its error rate rose. Absent on suspects recorded before failure attribution, which are latency suspects.
   */
  reason?: 'latency' | 'failures';
  baseline_error_rate_pct?: number;
  error_rate_pct?: number;
  error_type?: string;
};

