/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One of an app's release gates (ADR-20261019071734).
 */
export type ReleaseGate = {
  name: string;
  kind: 'slo' | 'new_errors' | 'custom_metric';
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
   * custom_metric gates: the custom metric.
   */
  metric?: string;
  /**
   * custom_metric gates: which side of the threshold fails.
   */
  comparison?: 'below' | 'above';
  /**
   * custom_metric gates: the bound on the metric's average.
   */
  threshold?: number;
  /**
   * Requests or metric values a release needs before the gate is judged.
   */
  min_samples: number;
  created_by: string;
  created_at: string;
};

