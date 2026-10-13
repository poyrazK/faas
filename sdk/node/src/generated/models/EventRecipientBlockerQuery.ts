/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Event and consumer identity with the original acceptance instant for a read-only lookup.
 */
export type EventRecipientBlockerQuery = {
  source: string;
  id: string;
  subscription_id: string;
  /**
   * Nonzero RFC3339 timestamp with its original fractional precision (up to nanoseconds).
   */
  expected_accepted_at: string;
};

