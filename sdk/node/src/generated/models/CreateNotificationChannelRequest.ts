/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Adds a Slack, PagerDuty or email alert channel (ADR-749). Set only the fields for the chosen kind.
 */
export type CreateNotificationChannelRequest = {
  /**
   * Unique per account.
   */
  name: string;
  /**
   * Destination type.
   */
  kind: 'slack' | 'pagerduty' | 'email';
  /**
   * Slack incoming webhook, https://hooks.slack.com/services/… (kind slack).
   */
  slack_webhook_url?: string;
  /**
   * Events API v2 integration key (kind pagerduty).
   */
  pagerduty_routing_key?: string;
  /**
   * PagerDuty service region (kind pagerduty).
   */
  pagerduty_region?: 'us' | 'eu';
  /**
   * The account's own email address (kind email).
   */
  email?: string;
};

