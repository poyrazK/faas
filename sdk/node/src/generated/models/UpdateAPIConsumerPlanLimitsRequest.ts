/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Replacement limits for a consumer plan. Omitting alert_thresholds_percent keeps the plan's thresholds; an empty list clears them.
 */
export type UpdateAPIConsumerPlanLimitsRequest = {
  max_requests_per_minute: number;
  max_units_per_month: number;
  /**
   * Replacement alert thresholds, as percentages of max_units_per_month (at most 5). Omit to keep the current thresholds; send an empty list to remove them.
   */
  alert_thresholds_percent?: Array<number>;
};

