/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Acceptance-time deferred acknowledgment configuration for an application subscription.
 */
export type EventAcknowledgmentPolicy = {
  /**
   * Subscription whose future acceptance policy is configured.
   */
  subscription_id: string;
  /**
   * Zero uses ordinary HTTP success; positive values enable deferred HTTP 202 processing.
   */
  lease_seconds: number;
};

