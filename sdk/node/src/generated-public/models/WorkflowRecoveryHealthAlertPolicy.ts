/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Opt-in recovery health incident notifications with explicit scheduler evaluation and sustained unhealthy duration thresholds. Settings are replaced with the full recovery policy. Read-only health query thresholds remain request-local.
 */
export type WorkflowRecoveryHealthAlertPolicy = {
  enabled: boolean;
  stall_after_seconds: number;
  alert_after_seconds: number;
};

