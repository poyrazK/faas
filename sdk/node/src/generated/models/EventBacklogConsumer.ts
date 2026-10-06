/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Exact counts over all matching waiting recipients for one captured app/subscription, independent of recipient pagination.
 */
export type EventBacklogConsumer = {
  app_id: string;
  app_slug: string;
  target_available: boolean;
  subscription_id: string;
  waiting_recipients: number;
  pending_recipients: number;
  processing_recipients: number;
  capacity_waiting_recipients: number;
  oldest_accepted_at: string;
  oldest_age_seconds: number;
};

