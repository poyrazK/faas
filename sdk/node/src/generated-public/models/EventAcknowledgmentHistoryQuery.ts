/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Scope-bound page options selecting coalesced groups by first occurrence.
 */
export type EventAcknowledgmentHistoryQuery = {
  /**
   * Delivery filter shared by every continuation request.
   */
  invocation_id?: string;
  /**
   * Outcome kind shared by the page traversal.
   */
  kind?: 'accepted' | 'renewed' | 'renewal_rejected' | 'completed' | 'expired' | 'retry_requested' | 'dead_letter_requested';
  /**
   * Inclusive first_at bound for the traversal.
   */
  from?: string;
  /**
   * Exclusive first_at bound for the traversal.
   */
  until?: string;
  /**
   * Page size included in the cursor scope binding.
   */
  limit?: number;
  /**
   * Previous page continuation retaining its initial cutoff.
   */
  cursor?: string;
};

