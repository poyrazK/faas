/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Whether recovery is expected and inside a pass, with its latest attempt/return clocks. Returning includes failed and no-op passes and does not prove a committed checkpoint, successful preparation or deployment readiness.
 */
export type ObsRuntimeHandoffRecoveryValues = {
  enabled: boolean;
  inflight: boolean;
  last_attempt_at?: string;
  last_completed_at?: string;
};

