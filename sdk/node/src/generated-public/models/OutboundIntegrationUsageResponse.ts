/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Current UTC-day admitted-request count and the effective optional request limit.
 */
export type OutboundIntegrationUsageResponse = {
  daily_request_count: number;
  daily_request_limit: number | null;
  usage_date: string;
  resets_at: string;
};

