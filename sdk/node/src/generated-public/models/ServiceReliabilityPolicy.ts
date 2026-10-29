/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Caller-owned timeout and retry settings for one declared internal dependency. Omitted numeric fields inherit gateway defaults; max_attempts=1 disables retry. POST/PATCH replay also requires an Idempotency-Key honored by the target.
 */
export type ServiceReliabilityPolicy = {
  timeout_ms?: number;
  max_attempts?: number;
  min_remaining_ms?: number;
  retry_budget_percent?: number;
  allow_non_idempotent?: boolean;
};

