/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Timing and optional reviewed version for a monthly draft schedule.
 */
export type PutDraftBillingRunScheduleRequest = {
  /**
   * Hours after month end when the schedule starts the run. Defaults to 24.
   */
  delay_hours?: number;
  /**
   * The reviewed draft schedule version. Omit to create only if there is no schedule.
   */
  expected_updated_at?: string;
};

