/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Ephemeral backend signal payload with optional name and expiration.
 */
export type ManagedRealtimeSignalRequest = {
  /**
   * Any JSON value, including null, within 2048 encoded UTF-8 bytes.
   */
  data: any;
  name?: string;
  /**
   * Requires name; defaults to 5000 for named signals. Zero clears.
   */
  ttl_ms?: number;
};

