/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * 201 — body of a freshly-enqueued queue row.
 */
export type QueueSendResponse = {
  id: string;
  /**
   * Canonical platform trace id when the request carried a valid trace context.
   */
  trace_id?: string;
};

