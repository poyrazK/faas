/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Replacement not-before instant for pending notification delivery work.
 */
export type ManagedRealtimeNotificationRescheduleRequest = {
  /**
   * RFC3339, up to 48 hours ahead; must precede expiration.
   */
  notification_not_before: string;
};

