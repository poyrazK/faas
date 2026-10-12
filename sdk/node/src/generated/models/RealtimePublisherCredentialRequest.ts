/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Publisher Credential Request.
 */
export type RealtimePublisherCredentialRequest = {
  /**
   * Message TTL cap; zero is uncapped. Positive caps require a positive message TTL at or below the cap.
   */
  max_message_ttl_seconds?: number;
  name: string;
  channels: Array<string>;
  event_types: Array<string>;
  publishes_per_minute?: number;
  max_payload_bytes?: number;
  ttl_seconds?: number;
};

