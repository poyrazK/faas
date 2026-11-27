/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Values captured at the first breach of a live observed incident.
 */
export type AlertObservedIncidentOpeningEvidence = {
  /**
   * Metric the rule evaluated when the incident opened.
   */
  metric: string;
  /**
   * Rolling metric window in use when the incident opened.
   */
  window: string;
  comparison: 'above_baseline' | 'below_baseline';
  /**
   * Observed value in the metric's units.
   */
  observed: number;
  /**
   * Historical median in the metric's units.
   */
  baseline: number;
  /**
   * Trigger derived from the baseline and rule threshold at opening.
   */
  trigger_value: number;
  /**
   * Number of days that qualified for the baseline.
   */
  baseline_days?: number;
};

