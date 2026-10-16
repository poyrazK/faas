/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Complete scoped advisory observation of all unexpired successful capacity-command receipts. Neither reserves receipt slots nor modifies retention. Known unchanged-input command repeats remain available when full; commands without UUIDs have no idempotent receipt evidence.
 */
export type EventSubscriptionCapacityCommandBudget = {
  /**
   * Owned application identifier. Context: Event Subscription Capacity Command Budget.
   */
  app_id: string;
  /**
   * Owned subscription or retained control identifier. Context: Event Subscription Capacity Command Budget.
   */
  subscription_id: string;
  /**
   * Logical observation time used for receipt expiry.
   */
  observed_at: string;
  /**
   * All matching unexpired receipts were counted rather than sampled from a history page.
   */
  complete: boolean;
  /**
   * Successful applied and unchanged command receipts whose retention deadline is after observed_at.
   */
  retained_receipts: number;
  /**
   * Maximum retained receipts per subscription.
   */
  limit: 128;
  /**
   * Limit minus retained receipts at the observation. Concurrent writes may consume slots.
   */
  remaining_slots: number;
  /**
   * Whether no slots for new command UUIDs were available at the observation.
   */
  full: boolean;
  /**
   * Earliest retained receipt deadline that could free a slot. Omitted when no receipts remain. Logical expiry frees quota without waiting for physical pruning.
   */
  earliest_expiry_at?: string;
  /**
   * Advisory guidance without a reservation or write authorization guarantee.
   */
  advice: string;
};

