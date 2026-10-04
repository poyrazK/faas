/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * 200 — a completed queue invocation observed by the long-poll feed. 204 (no body) on timeout.
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
  trace_id?: string;
  /**
   * W3C traceparent propagated from the producer.
   */
  traceparent?: string;
};

