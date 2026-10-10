/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Principal message payload with optional retained notification and push fallback settings.
 */
export type ManagedRealtimePrincipalMessageRequest = {
  principal: string;
  /**
   * Standard base64; at most 4096 decoded bytes.
   */
  data_base64: string;
  binary?: boolean;
  delivery?: 'live' | 'retained';
  message_id?: string;
  request_receipt?: boolean;
  /**
   * Requires retained delivery; zero disables fallback.
   */
  fallback_after_seconds?: number;
  /**
   * Stable conversation, job or project key; requires a fallback.
   */
  notification_group_key?: string;
  /**
   * Earliest built-in push delivery time; RFC3339 with timezone, at most 48 hours ahead. Requires fallback; explicit TTL must extend past this instant.
   */
  notification_not_before?: string;
  /**
   * Requires fallback. Newer queued alerts supersede older alerts for the same recipient, device, category, priority and collapse key.
   */
  notification_collapse_key?: string;
  /**
   * Push lifetime in seconds from publication. Positive values require fallback and must exceed its deadline. Zero or omission keeps default expiration.
   */
  notification_ttl_seconds?: number;
  /**
   * Defaults to normal when omitted; requires retained fallback. Urgent bypass requires user opt-in.
   */
  notification_priority?: 'low' | 'normal' | 'urgent';
  /**
   * Display name for summaries; requires a group key.
   */
  notification_group_label?: string;
  /**
   * Requires a nonzero fallback deadline; omitted values use notifications for push preferences.
   */
  notification_category?: string;
};

