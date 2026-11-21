/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explicit configured Task overrides only; no binding or readiness evidence.
 */
export type TaskReviewLimits = {
  ttl_seconds?: number;
  worker_concurrency?: number;
  max_attempts?: number;
  max_running?: number;
  max_running_per_owner?: number;
  max_outstanding?: number;
  max_outstanding_per_owner?: number;
  shutdown_timeout_ms?: number;
};

