/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Complete mutually exclusive effective waiting reasons across retained pending application recipients in the backlog snapshot. Counts sum to pending_recipients when complete, independently of recipient pagination. Missing evidence from older servers is unavailable, not zero.
 */
export type EventConsumerWaitingReasons = {
  app_id: string;
  subscription_id: string;
  observed_at: string;
  /**
   * Acceptance cutoff used by the backlog query.
   */
  window_at: string;
  coverage: 'all_pending_application_recipients';
  complete: boolean;
  pending_recipients: number;
  /**
   * Counts for circuit_open, circuit_probe_wait, circuit_recovery_rate_limited, subscription_paused, subscription_rate_limited, ordering_blocked, capacity_consumer, capacity_app, capacity_account, routing_in_progress, receipt_processing, retry_backoff, workflow_routing, and ready. Known reasons include zero counts; processing recipients are excluded.
   */
  counts: Record<string, number>;
};

