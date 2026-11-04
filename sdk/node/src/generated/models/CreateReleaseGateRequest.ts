/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Adds a release gate. Give exactly one kind's fields; omitted numbers take Gregale's defaults.
 */
export type CreateReleaseGateRequest = {
  name: string;
  kind: 'slo' | 'new_errors' | 'custom_metric' | 'ai' | 'synthetic' | 'metric_change';
  /**
   * slo gates: the name of one of the app's SLOs.
   */
  slo?: string;
  /**
   * slo gates: default 10.
   */
  max_burn_rate?: number;
  /**
   * new_errors gates: default 0.
   */
  max_new_groups?: number;
  /**
   * new_errors gates: default 3.
   */
  min_events?: number;
  /**
   * custom_metric and metric_change gates: the custom metric's name. ai gates: error_rate, flagged_rate, cost_per_call or ttfb_ms.
   */
  metric?: string;
  /**
   * custom_metric gates: fail when the average is below or above the threshold. metric_change gates: fail when the average fell (below) or rose (above) too far.
   */
  comparison?: 'below' | 'above';
  /**
   * custom_metric gates: the bound.
   */
  threshold?: number;
  /**
   * ai gates: fail when the release's metric is this many percent above its baseline's, and above a floor (1 percentage point, 50 ms or 1 millicent). Default 50.
   */
  max_increase_percent?: number;
  /**
   * synthetic gates: the name of one of the app's synthetic checks.
   */
  check?: string;
  /**
   * synthetic gates: default 10.
   */
  max_failure_percent?: number;
  /**
   * metric_change gates: the change, in percent of the average before the rollout, that fails the gate.
   */
  max_change_percent?: number;
  /**
   * Default 20; 6 probes for synthetic gates.
   */
  min_samples?: number;
};

