/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { InsightEventsQuery } from './InsightEventsQuery.js';
import type { InsightFunnelRequest } from './InsightFunnelRequest.js';
import type { InsightRetentionRequest } from './InsightRetentionRequest.js';
import type { SavedInsightAlert } from './SavedInsightAlert.js';
/**
 * A saved insight: exactly the query of its kind, without from or until, over the last range_days days. A digest or alert needs at least one notification channel.
 */
export type SavedInsightRequest = {
  key: string;
  /**
   * Display name; defaults to the key.
   */
  name?: string;
  kind: 'events' | 'funnel' | 'retention';
  events?: InsightEventsQuery;
  funnel?: InsightFunnelRequest;
  retention?: InsightRetentionRequest;
  /**
   * Days each run covers; default 7 (28 for retention), at most the plan's analytics retention.
   */
  range_days?: number;
  /**
   * Send the value to the channels at 08:00 UTC, weekly on Mondays. PagerDuty channels get no digests.
   */
  digest?: 'daily' | 'weekly';
  alert?: SavedInsightAlert;
  /**
   * The account's notification channels for digests and alerts.
   */
  channel_ids?: Array<string>;
};

