/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explicit replacement of a subscription policy for future acceptances.
 */
export type EventAcknowledgmentPolicyRequest = {
  /**
   * Requested processing lease duration; zero disables deferred acknowledgment for future acceptances.
   */
  lease_seconds: number;
};

