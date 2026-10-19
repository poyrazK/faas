/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One alert notification channel (ADR-749).
 */
export type NotificationChannelResponse = {
  id: string;
  name: string;
  kind: 'slack' | 'pagerduty' | 'email';
  /**
   * Non-secret hint: Slack workspace and hook ids, the routing key last four characters, or the email address.
   */
  target: string;
  /**
   * Region of a PagerDuty channel.
   */
  pagerduty_region?: 'us' | 'eu';
  /**
   * Last successful delivery or test.
   */
  last_delivered_at?: string;
  /**
   * Most recent delivery error, if any.
   */
  last_error?: string;
  /**
   * When last_error occurred.
   */
  last_error_at?: string;
  created_at: string;
};

