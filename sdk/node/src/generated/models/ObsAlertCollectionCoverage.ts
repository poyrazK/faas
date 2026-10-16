/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Exact prospective cadence-evidence partition of a requested retained window; no continuous-availability claim.
 */
export type ObsAlertCollectionCoverage = {
  since: string;
  as_of: string;
  scope: 'recorded_collection_cadence';
  /**
   * Observed means every interval has recent successful outcome evidence; partial includes failed or missing intervals; unknown has no fresh duration evidence.
   */
  state: 'observed' | 'partial' | 'unknown';
  max_age_seconds: number;
  retention_seconds: number;
  window_microseconds: number;
  successful_observation_microseconds: number;
  failed_observation_microseconds: number;
  unknown_microseconds: number;
  successes: number;
  failures: number;
  limitations: Array<string>;
};

