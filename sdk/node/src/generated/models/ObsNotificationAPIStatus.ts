/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Reachability and bounded validity of the local alert-status API; does not establish notification receiver readiness.
 */
export type ObsNotificationAPIStatus = {
  state: 'healthy' | 'disabled' | 'unavailable';
  reason?: string;
  observed_at?: string;
};

