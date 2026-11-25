/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An environment's flag notification channels.
 */
export type FlagNotificationSettings = {
  /**
   * Notification channel IDs; empty when the environment's flag notifications are off.
   */
  channel_ids: Array<string>;
  /**
   * The account that last changed them.
   */
  updated_by?: string;
  updated_at?: string;
};

