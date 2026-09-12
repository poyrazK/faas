/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Create a durable managed realtime endpoint and its callback contract.
 */
export type CreateManagedRealtimeEndpointRequest = {
  callback_url: string;
  connect_path?: string;
  message_path?: string;
  disconnect_path?: string;
  callback_auth_token: string;
  auth_token?: string;
  enabled?: boolean;
};

