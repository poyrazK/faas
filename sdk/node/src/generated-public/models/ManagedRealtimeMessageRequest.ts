/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Binary-safe message payload encoded as standard base64.
 */
export type ManagedRealtimeMessageRequest = {
  /**
   * Decoded payload is limited to 1 MiB.
   */
  data_base64: string;
  binary?: boolean;
};

