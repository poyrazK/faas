/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One tag's UTC-day AI usage on an integration; tokens is input plus output. A key's tags past the day's cap of distinct tags add to its overflow row, <key>=~other.
 */
export type OutboundAITagUsageItem = {
  tag: string;
  requests: number;
  error_requests: number;
  input_tokens: number;
  output_tokens: number;
  tokens: number;
  cost_millicents: number;
  daily_cost_limit_millicents: number | null;
};

