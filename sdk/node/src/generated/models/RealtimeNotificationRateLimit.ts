/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Shared provider-delivery quota for a principal across registered devices.
 */
export type RealtimeNotificationRateLimit = {
  max_notifications: number;
  window_seconds: 60 | 300 | 3600;
  /**
   * Allow urgent alerts to bypass only this quota.
   */
  allow_urgent_bypass?: boolean;
};

