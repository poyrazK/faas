/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A committed message and its channel-scoped sequence.
 */
export type ManagedRealtimeRetainedMessageResponse = {
  /**
   * Exact-match routing metadata; at most 4096 encoded JSON bytes. Retained channel publishing only.
   */
  metadata?: Record<string, string>;
  sequence: number;
  data_base64: string;
  binary: boolean;
  created_at: string;
};

