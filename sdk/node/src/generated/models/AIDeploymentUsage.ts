/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One deployment's AI gateway calls over the last 14 days.
 */
export type AIDeploymentUsage = {
  deployment_id: string;
  /**
   * The UTC minute of its first call in the window.
   */
  first_call_at: string;
  /**
   * The UTC minute of its latest call.
   */
  last_call_at: string;
  calls: number;
  /**
   * Calls the provider failed.
   */
  error_calls: number;
  /**
   * Calls whose answer had a quality flag.
   */
  flagged_calls: number;
  cost_millicents: number;
  input_tokens: number;
  output_tokens: number;
  error_rate: number;
  flagged_rate: number;
  cost_per_call_millicents: number;
  /**
   * Mean time to first byte; omitted when no call had one.
   */
  mean_ttfb_ms?: number;
};

