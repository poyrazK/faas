/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Current operation timestamp required to pause, resume or abort retained rollout intent.
 */
export type ControlApplicationStandardOperationRequest = {
  /**
   * Exact nonzero updated_at from the current operation, with at most microsecond precision; never round a stale token.
   */
  expected_updated_at: string;
};

