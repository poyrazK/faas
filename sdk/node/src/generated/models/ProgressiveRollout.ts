/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Health-gated, operator-promoted stages for a boolean true rule. The rule rollout must equal stages[current_stage]; stages must strictly increase and end at 10000 basis points.
 */
export type ProgressiveRollout = {
  /**
   * Strictly increasing rollout percentages in basis points
   */
  stages: Array<number>;
  /**
   * Zero-based active stage index.
   */
  current_stage: number;
  /**
   * Minimum application-reported used requests for the targeted rule before promotion.
   */
  minimum_used_requests: number;
  /**
   * Highest allowed 5xx rate for the targeted rule; 100 basis points is one percent.
   */
  maximum_http_5xx_rate_basis_points: number;
  /**
   * Highest allowed conservative p95 latency bucket bound for the targeted rule.
   */
  maximum_p95_latency_ms: number;
  /**
   * Evidence lookback window; must fit within debugger retention.
   */
  window_seconds: number;
};

