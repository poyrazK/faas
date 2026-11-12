/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Renames a channel or replaces its destination; set name, the channel's own destination, or both.
 */
export type UpdateNotificationChannelRequest = {
  /**
   * New name, unique per account.
   */
  name?: string;
  /**
   * New Slack incoming webhook (kind slack).
   */
  slack_webhook_url?: string;
  /**
   * New Events API v2 integration key (kind pagerduty).
   */
  pagerduty_routing_key?: string;
  /**
   * PagerDuty service region of the new key (kind pagerduty); default us.
   */
  pagerduty_region?: 'us' | 'eu';
};

