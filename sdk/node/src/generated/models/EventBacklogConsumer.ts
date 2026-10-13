/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Exact counts over all matching waiting recipients for one app/subscription/consumer kind, independent of recipient pagination.
 */
export type EventBacklogConsumer = {
  /**
   * Exact mutually exclusive effective waiting-reason counts over matching pending recipients, including zero values for known reasons. Independent of recipient pagination.
   */
  pending_waiting_reasons?: Record<string, number>;
  /**
   * Complete count with effective capacity_consumer waiting reason.
   */
  capacity_consumer_waiting_recipients?: number;
  /**
   * Complete count with effective capacity_app waiting reason.
   */
  capacity_app_waiting_recipients?: number;
  /**
   * Complete count with effective capacity_account waiting reason.
   */
  capacity_account_waiting_recipients?: number;
  app_id: string;
  app_slug: string;
  target_available: boolean;
  subscription_id: string;
  consumer_kind: 'application' | 'workflow';
  waiting_recipients: number;
  pending_recipients: number;
  processing_recipients: number;
  capacity_waiting_recipients: number;
  ordering_waiting_recipients: number;
  oldest_accepted_at: string;
  oldest_age_seconds: number;
};

