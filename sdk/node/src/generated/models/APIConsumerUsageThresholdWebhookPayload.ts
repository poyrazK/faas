/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Data delivered with consumer.usage_threshold when a consumer crosses a plan alert threshold. alert_id is stable across retries.
 */
export type APIConsumerUsageThresholdWebhookPayload = {
  alert_id: string;
  app_id: string;
  consumer_id: string;
  external_ref: string;
  plan_id: string;
  threshold_percent: number;
  limit_units: number;
  used_units: number;
  month_start: string;
  crossed_at: string;
};

