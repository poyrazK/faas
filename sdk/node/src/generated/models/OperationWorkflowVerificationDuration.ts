/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Publication-based proof waiting time and coverage counts for one verification owner within an instance.
 */
export type OperationWorkflowVerificationDuration = {
  /**
   * Application-assigned verification owner. Empty means unassigned.
   */
  owner: string;
  observed_seconds: number;
  resolution_count: number;
  pending_count: number;
  /**
   * Retained obligations whose start report is outside the history window. No wait duration is inferred for these obligations.
   */
  unknown_start_count: number;
};

