/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Opt-in thresholds for scheduler-enforced failure admission pausing.
 */
export type AutomationFailurePolicy = {
  /**
   * Monotonic failure monitoring policy revision; zero means no policy has been configured.
   */
  version: number;
  /**
   * Whether scheduler evaluation can create a new runtime failure pause.
   */
  enabled: boolean;
  /**
   * Terminal failures required to latch a failure pause.
   */
  failure_threshold: number;
  /**
   * Minimum terminal non-cancelled sample count before failure pausing.
   */
  min_completed_runs: number;
  /**
   * Lookback in seconds, bounded by the latest monitoring epoch.
   */
  window_seconds: number;
};

