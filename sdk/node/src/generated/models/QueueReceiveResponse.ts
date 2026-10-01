/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * 200 — a completed queue invocation observed by the long-poll feed. 204 (no body) on timeout.
 */
export type QueueReceiveResponse = {
  /**
   * Deployment scope captured when the message was accepted.
   */
  environment?: string;
  /**
   * Immutable captured queue binding identity; omitted for legacy unbound work.
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

