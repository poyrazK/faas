/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable collection receipt and explicit producer configuration or health state.
 */
export type ObsAlertCollectionHealth = {
  state: 'current' | 'unknown';
  collection_state?: 'disabled' | 'never_collected' | 'current' | 'failed' | 'stale' | 'unknown';
  reason?: string;
  last_attempt_at?: string;
  last_success_at?: string;
};

