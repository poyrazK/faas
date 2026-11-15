/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One version's calls, errors, latency, cost and feedback over the stats window.
 */
export type OutboundAIPromptVersionStats = {
  version: number;
  calls: number;
  errors: number;
  error_rate: number;
  mean_ttfb_ms: number | null;
  cost_millicents: number;
  mean_cost_millicents: number | null;
  good_feedback: number;
  bad_feedback: number;
};

