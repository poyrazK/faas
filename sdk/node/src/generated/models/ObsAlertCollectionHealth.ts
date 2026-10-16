/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable collection receipt and explicit producer configuration or health state.
 */
export type ObsAlertCollectionHealth = {
  /**
   * Known private diagnostics route advertised as an optional bundle feature marker; reads do not establish past failure causation.
   */
  diagnostics_path?: string;
  state: 'current' | 'unknown';
  collection_state?: 'disabled' | 'never_collected' | 'current' | 'failed' | 'stale' | 'unknown';
  reason?: string;
  last_attempt_at?: string;
  last_success_at?: string;
};

