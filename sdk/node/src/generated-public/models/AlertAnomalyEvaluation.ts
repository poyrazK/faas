/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Why the latest anomaly evaluation did not trigger a fire.
 */
export type AlertAnomalyEvaluation = {
  /**
   * Number of days that qualified for the baseline when history was evaluated.
   */
  baseline_days?: number;
  reason: 'insufficient_traffic' | 'insufficient_history' | 'telemetry_missing_or_stale' | 'telemetry_unavailable' | 'threshold_not_reached' | 'minimum_impact_not_reached' | 'zero_baseline' | 'notification_cooldown' | 'observed_breach' | 'recovery_confirmation_pending' | 'recovery_confirmed';
  evaluated_at: string;
  /**
   * Eligible requests, qualifying historical windows, or consecutive healthy checks depending on the reason.
   */
  count: number;
  /**
   * Required requests, historical windows, or healthy checks when the reason includes a count.
   */
  required_count?: number;
  /**
   * Observed value in the rule metric's units for a completed check.
   */
  observed?: number;
  /**
   * Historical median in the rule metric's units for a completed check.
   */
  baseline?: number;
  /**
   * Baseline multiplied by the rule threshold when a positive baseline is available.
   */
  trigger_value?: number;
  /**
   * Minimum required observation for an above-baseline rule, or minimum required baseline for a below-baseline rule. Present when that minimum was not reached.
   */
  minimum_value?: number;
  /**
   * End of the current incident's notification cooldown when the evaluation suppressed a repeat fire.
   */
  next_notification_eligible_at?: string;
};

