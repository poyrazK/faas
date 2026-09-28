/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Current queue counts and terminal outcomes over the 24 hours ending at snapshot_at.
 */
export type AppWebhookDeliveryHealthResponse = {
  webhook_id: string;
  snapshot_at: string;
  pending_count: number;
  in_flight_count: number;
  dead_count: number;
  /**
   * Active receiver Retry-After deadline; new claims for this subscription resume when it expires.
   */
  receiver_cooldown_until?: string;
  /**
   * Earliest due time among claimable pending or expired in-flight deliveries; omitted during an active receiver cooldown.
   */
  oldest_overdue_at?: string;
  /**
   * Age of the oldest overdue delivery at snapshot_at.
   */
  oldest_overdue_seconds?: number;
  recent_succeeded_count: number;
  recent_dead_count: number;
  /**
   * Recent succeeded divided by recent succeeded plus dead; omitted when there are no terminal deliveries.
   */
  recent_success_rate?: number;
};
