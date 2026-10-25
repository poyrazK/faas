/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Sampled latency and error measurements for one route dependency under a single immutable deployment, with an advisory comparison to a prior sampled revision when possible.
 */
export type RequestAnalyticsDependencyDeploymentObservation = {
  /**
   * Immutable deployment owning these sampled dependency observations.
   */
  deployment_id: string;
  commit_sha?: string;
  deployment_tag?: string;
  /**
   * RFC 3339 creation time used to order comparable revisions.
   */
  deployment_created_at?: string;
  /**
   * Retained dependency span observations attached to this deployment.
   */
  samples: number;
  /**
   * Observed span sample count weighted by collapsed request-row count.
   */
  calls: number;
  error_calls: number;
  /**
   * Error share among this deployment's observed weighted calls.
   */
  error_rate_pct: number;
  p50_ms: number;
  p95_ms: number;
  p99_ms: number;
  exclusive_p95_ms: number;
  /**
   * P95 percentage change from the preceding comparable deployment, when its baseline is non-zero.
   */
  p95_change_pct?: number;
  /**
   * Error-rate change in percentage points from the preceding comparable deployment.
   */
  error_rate_change_pct?: number;
  /**
   * Tag, commit SHA, or deployment UUID used as the preceding revision baseline.
   */
  compared_to?: string;
  /**
   * True when p95 rose by at least 25% or error rate rose by at least 2 percentage points.
   */
  regression: boolean;
};

