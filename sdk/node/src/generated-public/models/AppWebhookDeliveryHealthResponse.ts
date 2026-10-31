/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Receiver recovery state, claimable queue age, and terminal outcomes over the 24 hours ending at snapshot_at.
 */
export type AppWebhookDeliveryHealthResponse = {
  webhook_id: string;
  snapshot_at: string;
  pending_count: number;
  in_flight_count: number;
  dead_count: number;
  /**
   * Claim gate at snapshot_at; probing requires a live recovery delivery lease.
   */
  receiver_state: 'ready' | 'cooling_down' | 'awaiting_probe' | 'probing';
  /**
   * Active receiver pause deadline; after it expires, one recovery delivery probes before normal capacity resumes.
   */
  receiver_cooldown_until?: string;
  /**
   * Earliest due time among claimable pending or expired in-flight deliveries; omitted while the subscription has no claim capacity.
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

