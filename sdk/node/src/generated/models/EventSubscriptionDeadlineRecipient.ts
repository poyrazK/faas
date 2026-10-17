/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Payload-free identity, effective deadline and observed waiting reason of a retained pending delivery. The expiry flag uses the current page observation, not the fixed status-filter window.
 */
export type EventSubscriptionDeadlineRecipient = {
  event_source: string;
  event_id: string;
  accepted_at: string;
  deadline_at: string;
  expired: boolean;
  waiting_reason: 'circuit_open' | 'circuit_probe_wait' | 'circuit_recovery_rate_limited' | 'subscription_paused' | 'subscription_rate_limited' | 'ordering_blocked' | 'capacity_consumer' | 'capacity_app' | 'capacity_account' | 'routing_in_progress' | 'receipt_processing' | 'retry_backoff' | 'workflow_routing' | 'ready';
  next_attempt_at?: string;
  lease_until?: string;
};

