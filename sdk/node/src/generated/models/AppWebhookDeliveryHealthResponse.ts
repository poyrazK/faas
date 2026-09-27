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
   * Earliest due time among pending or expired in-flight deliveries.
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

