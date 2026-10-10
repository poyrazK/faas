/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Complete failure policy replacement guarded by an expected version.
 */
export type SetAutomationFailurePolicyRequest = {
  /**
   * Failure policy revision the caller intends to replace.
   */
  expected_version: number;
  /**
   * Requested setting controlling whether scheduler evaluation can create a new runtime failure pause.
   */
  enabled: boolean;
  /**
   * Requested number of terminal failures required to latch a failure pause.
   */
  failure_threshold: number;
  /**
   * Requested minimum terminal non-cancelled sample count before failure pausing.
   */
  min_completed_runs: number;
  /**
   * Requested lookback in seconds, bounded by the latest monitoring epoch.
   */
  window_seconds: number;
};

