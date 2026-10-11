/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Recorded notification delivery or lifecycle change for a principal.
 */
export type ManagedRealtimeNotificationTimelineEvent = {
  id: number;
  message_id: string;
  device?: string;
  delivery_id?: string;
  /**
   * Delivery status or fallback_scheduled, fallback_rescheduled, fallback_removed.
   */
  event: string;
  reason?: string;
  attempts: number;
  status_code: number;
  occurred_at: string;
  not_before: string;
  next_attempt: string;
};

