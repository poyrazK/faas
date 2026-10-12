/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Managed Realtime Access Request.
 */
export type ManagedRealtimeAccessRequest = {
  action: 'revoke_channel' | 'restore_channel' | 'revoke_sessions';
  /**
   * Required for channel actions; omitted for revoke_sessions.
   */
  channel?: string;
};

