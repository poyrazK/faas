/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable receipt for a queued app-to-app message.
 */
export type SendAppMessageResponse = {
  /**
   * Durable invocation identifier.
   */
  id: string;
  event_id: string;
  target_app: string;
  status: 'pending';
  status_url: string;
  trace_id?: string;
};

