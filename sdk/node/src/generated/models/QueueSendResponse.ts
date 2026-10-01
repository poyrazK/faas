/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * 201 — body of a freshly-enqueued queue row.
 */
export type QueueSendResponse = {
  /**
   * Deployment scope captured when the message was accepted.
   */
  environment?: string;
  /**
   * Immutable captured queue binding identity; omitted for legacy unbound work.
   */
  queue_binding_id?: string;
  id: string;
  /**
   * Canonical platform trace id when the request carried a valid trace context.
   */
  trace_id?: string;
};

