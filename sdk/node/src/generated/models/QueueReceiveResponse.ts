/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * 200 — a settled queue invocation observed by the long-poll feed: every waiting receive sees it, and one that settles while none waits is not replayed. 204 (no body) on timeout. Consume with pull leases instead.
 */
export type QueueReceiveResponse = {
  /**
   * Original environment of the completed invocation in this queue feed.
   */
  environment?: string;
  /**
   * Retained binding identity of the completed queue invocation; absent for legacy work.
   */
  queue_binding_id?: string;
  id: string;
  payload: Record<string, any>;
  result?: Record<string, any>;
  /**
   * How the message settled: completed, failed or cancelled. A failed or cancelled message's result is its error.
   */
  state?: string;
  trace_id?: string;
  /**
   * W3C traceparent propagated from the producer.
   */
  traceparent?: string;
};

