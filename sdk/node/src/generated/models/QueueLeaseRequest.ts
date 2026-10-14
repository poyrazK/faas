/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * ADR-1140 pull lease. Zero values lease one message for the default visibility timeout (30s) and return at once.
 */
export type QueueLeaseRequest = {
  /**
   * A lease also stops once its payloads and headers would pass 8 MiB; it always returns at least one ready message.
   */
  max_messages?: number;
  /**
   * At most the plan's async invocation deadline.
   */
  visibility_timeout_seconds?: number;
  /**
   * When no message is ready, hold the call until one arrives or this many seconds pass, then return an empty list.
   */
  wait_seconds?: number;
};

