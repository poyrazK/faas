/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One of an app's release gates.
 */
export type ReleaseGate = {
  name: string;
  kind: 'slo' | 'new_errors' | 'custom_metric' | 'ai' | 'synthetic' | 'metric_change';
  /**
   * slo gates: the SLO judged.
   */
  slo?: string;
  /**
   * slo gates: the burn rate that fails the gate.
   */
  max_burn_rate?: number;
  /**
   * new_errors gates: new error groups allowed.
   */
  max_new_groups?: number;
  /**
   * new_errors gates: events before a group counts.
   */
  min_events?: number;
  /**
   * custom_metric and metric_change gates: the custom metric. ai gates: error_rate, flagged_rate, cost_per_call or ttfb_ms.
   */
  metric?: string;
  /**
   * custom_metric gates: which side of the threshold fails; metric_change gates: which direction of change fails.
   */
  comparison?: 'below' | 'above';
  /**
   * custom_metric gates: the bound on the metric's average.
   */
  threshold?: number;
  /**
   * ai gates: the largest increase over the baseline, in percent.
   */
  max_increase_percent?: number;
  /**
   * synthetic gates: the synthetic check probed on the canary and its stable revision.
   */
  check?: string;
  /**
   * synthetic gates: the share of the candidate's probes that may fail.
   */
  max_failure_percent?: number;
  /**
   * metric_change gates: how far the metric may move from its average before the rollout, in percent.
   */
  max_change_percent?: number;
  /**
   * Requests, probes, metric values or AI calls (on each side, for ai gates) needed before the gate is judged.
   */
  min_samples: number;
  created_by: string;
  created_at: string;
};

