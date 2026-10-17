/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One target's settled attempts in a canary's or a strategy's window. Errors are attempts with a provider status of 400 or above; mean_ttfb_ms averages successful attempts that received a first byte, and mean_cost_millicents is the cost per successful attempt; each is null without one.
 */
export type OutboundAIRouteTargetStats = {
  target: number;
  calls: number;
  errors: number;
  error_rate: number;
  mean_ttfb_ms: number | null;
  cost_millicents: number;
  mean_cost_millicents: number | null;
};

