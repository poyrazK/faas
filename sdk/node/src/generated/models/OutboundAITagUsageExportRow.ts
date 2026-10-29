/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One NDJSON line of a tag usage export; CSV rows carry the same fields in this order.
 */
export type OutboundAITagUsageExportRow = {
  usage_date: string;
  tag: string;
  requests: number;
  error_requests: number;
  input_tokens: number;
  output_tokens: number;
  tokens: number;
  cost_millicents: number;
};

