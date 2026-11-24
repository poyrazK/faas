/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Metadata after the predecessor static bearer credential is revoked.
 */
export type FinalizeManagedRealtimeAuthResponse = {
  endpoint_id: string;
  auth_mode: 'static_bearer';
  previous_token_expires_at: string | null;
};

