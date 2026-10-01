/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Health-gate evaluation for a progressive rollout stage. A held or complete status does not publish a new configuration version.
 */
export type FlagRolloutPromotion = {
  status: 'held' | 'promoted' | 'complete';
  reason?: 'insufficient_used_requests' | 'http_5xx_rate_exceeded' | 'p95_latency_exceeded' | 'all_stages_complete';
  flag: string;
  rule_id: string;
  /**
   * Current version after promotion
   */
  config_version: number;
  /**
   * One-based ordinal of the currently configured stage.
   */
  current_stage: number;
  stage_count: number;
  /**
   * Active rollout percentage in basis points.
   */
  rollout_basis_points: number;
  /**
   * Next configured percentage after promotion, omitted when already at the final stage.
   */
  next_rollout_basis_points?: number;
  request_count: number;
  used_count: number;
  http_5xx_count: number;
  /**
   * Observed 5xx count divided by the target rule request count.
   */
  http_5xx_rate: number;
  p95_latency_ms: number;
  /**
   * True when p95 came from stored bucket bounds; false when no matching evidence was available.
   */
  latency_quantized: boolean;
  minimum_used_requests: number;
  maximum_http_5xx_rate_basis_points: number;
  maximum_p95_latency_ms: number;
  window_start?: string;
  window_end?: string;
};

