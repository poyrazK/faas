/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One captured application recipient awaiting routing, without envelope data.
 */
export type EventBacklogRecipient = {
  event_source: string;
  event_id: string;
  event_type: string;
  accepted_at: string;
  app_id: string;
  /**
   * Empty when the captured app is no longer owned by this account.
   */
  app_slug: string;
  target_available: boolean;
  subscription_id: string;
  routing_mode: 'event' | 'recipient';
  state: 'pending' | 'processing';
  /**
   * Last recorded capacity scope for a currently pending recipient.
   */
  capacity_scope?: 'consumer' | 'app' | 'account';
  attempts: number;
  capacity_deferrals: number;
  next_attempt_at?: string;
  /**
   * Recipient lease or shared whole-receipt lease.
   */
  lease_until?: string;
  /**
   * Age from acceptance at observed_at, not time spent at the latest capacity scope.
   */
  pending_age_seconds: number;
  /**
   * Recorded capacity takes precedence for pending recipients; receipt_processing refers only to a shared receipt lease.
   */
  waiting_reason: 'capacity_consumer' | 'capacity_app' | 'capacity_account' | 'routing_in_progress' | 'receipt_processing' | 'retry_backoff' | 'ready';
  receipt_url: string;
  /**
   * Present when the captured target still belongs to the account. History coverage is independently bounded.
   */
  fanout_history_url?: string;
};

