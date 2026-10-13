/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * ADR-1140 pull lease. Zero values lease one message for the default visibility timeout (30s).
 */
export type QueueLeaseRequest = {
  max_messages?: number;
  /**
   * At most the plan's async invocation deadline.
   */
  visibility_timeout_seconds?: number;
};

