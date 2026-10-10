/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Named consumer plan with enforcement limits.
 */
export type APIConsumerPlanResponse = {
  id: string;
  app_id: string;
  name: string;
  max_requests_per_minute: number;
  max_units_per_month: number;
  /**
   * The plan's usage alert thresholds, ascending; empty means no alerts.
   */
  alert_thresholds_percent: Array<number>;
  created_at: string;
  updated_at: string;
};

