/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Publisher Credential.
 */
export type RealtimePublisherCredential = {
  /**
   * Immutable message TTL cap; zero is uncapped. Rotation preserves this policy.
   */
  max_message_ttl_seconds?: number;
  name: string;
  channels: Array<string>;
  event_types: Array<string>;
  publishes_per_minute: number;
  max_payload_bytes: number;
  id: string;
  endpoint_id: string;
  generation: string;
  issued_at: string;
  expires_at: string;
  revoked_at: string | null;
  accepted_count: string;
  replay_count: string;
  last_used_at: string | null;
  last_channel: string | null;
  last_sequence: string | null;
};

