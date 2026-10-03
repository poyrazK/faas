/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * 201 — body of a freshly-enqueued queue row.
 */
export type QueueSendResponse = {
  /**
   * Environment recorded for the newly accepted queue message.
   */
  environment?: string;
  /**
   * Binding identity captured for this accepted message; absent for unbound legacy sends.
   */
  queue_binding_id?: string;
  id: string;
  /**
   * Canonical platform trace id when the request carried a valid trace context.
   */
  trace_id?: string;
};

