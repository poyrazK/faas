/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Whole-retention episode totals and an attention union that counts each episode once.
 */
export type ObsAlertWorkloadCounts = {
  total: number;
  firing: number;
  unknown: number;
  resolved: number;
  /**
   * Unsnoozed unresolved episodes that are unacknowledged or unowned; unknown status is included.
   */
  needs_attention: number;
};

