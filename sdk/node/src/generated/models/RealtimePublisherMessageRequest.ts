/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Publisher Message Request.
 */
export type RealtimePublisherMessageRequest = {
  /**
   * Message lifetime from retained publication; zero disables expiry. Positive credential caps forbid zero or omitted TTL.
   */
  ttl_seconds?: number;
  event_type: string;
  data_base64: string;
  binary?: boolean;
  /**
   * event_type is reserved; supplied value must match the top-level event_type.
   */
  metadata?: Record<string, string>;
};

