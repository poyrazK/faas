/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Start or replace a prompt canary. Omitted margins take the route canary defaults.
 */
export type PutOutboundAIPromptCanaryRequest = {
  version: number;
  percent: number;
  max_error_rate_increase?: number;
  max_latency_increase_percent?: number;
  min_calls?: number;
  window_minutes?: number;
};

