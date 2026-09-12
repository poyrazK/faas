/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable managed realtime endpoint configuration. Credentials are
 * write-only and represented by a constant mask on every response.
 *
 */
export type ManagedRealtimeEndpointResponse = {
  id: string;
  app_id: string;
  account_id: string;
  callback_url: string;
  connect_path: string;
  message_path: string;
  disconnect_path: string;
  callback_auth_token_masked: '***';
  auth_token_masked: '***';
  enabled: boolean;
  created_at: string;
  updated_at: string;
};

