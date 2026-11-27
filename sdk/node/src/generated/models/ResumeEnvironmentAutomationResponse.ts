/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Crons, event subscriptions and bucket notifications enabled by resuming a stage's automation after a full environment clone.
 */
export type ResumeEnvironmentAutomationResponse = {
  /**
   * Stage whose paused automation was enabled.
   */
  environment: string;
  /**
   * Number of crons enabled.
   */
  crons_resumed: number;
  /**
   * Crons enabled by this call.
   */
  cron_ids: Array<string>;
  /**
   * Number of event subscriptions enabled.
   */
  event_subscriptions_resumed: number;
  /**
   * Event subscriptions enabled by this call.
   */
  event_subscription_ids: Array<string>;
  /**
   * Number of buckets whose copied notification rules were resumed.
   */
  bucket_notifications_resumed: number;
  /**
   * Buckets whose notification rules this call resumed.
   */
  bucket_ids: Array<string>;
};

