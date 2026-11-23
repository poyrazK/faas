/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Safe control-plane projection of one live managed realtime connection.
 */
export type ManagedRealtimeConnectionResponse = {
  id: string;
  endpoint_id: string;
  app_id: string;
  account_id: string;
  principal?: string;
  connected_at: string;
  last_seen_at: string;
  expires_at: string;
  channels?: Array<string>;
};

