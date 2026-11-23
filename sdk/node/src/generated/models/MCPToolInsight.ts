/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One tool's calls, failures, argument mismatches, compute and latency.
 */
export type MCPToolInsight = {
  tool: string;
  calls: number;
  errors: number;
  cancellations: number;
  /**
   * Calls whose arguments did not match the tool's input schema; also counted in errors.
   */
  mismatches: number;
  execution_seconds: number;
  gb_seconds: number;
  /**
   * Upper bound at the overage rate, in 1/100,000 USD.
   */
  estimated_millicents: number;
  p50_ms?: number;
  p95_ms?: number;
  p99_ms?: number;
};

