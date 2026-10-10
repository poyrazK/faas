/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One plan alert threshold a consumer crossed in one UTC month (ADR-849).
 */
export type APIConsumerUsageAlertResponse = {
  id: string;
  consumer_id: string;
  plan_id: string;
  month_start: string;
  threshold_percent: number;
  limit_units: number;
  /**
   * Weighted units used this month when the threshold was crossed.
   */
  used_units: number;
  crossed_at: string;
};

