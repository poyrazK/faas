/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventFanoutAttemptResponse } from './EventFanoutAttemptResponse.js';
import type { EventFanoutHistorySummaryResponse } from './EventFanoutHistorySummaryResponse.js';
/**
 * Bounded recorded routing outcomes with durable recipient summaries. Repeated capacity waits coalesce; details have row and logical byte caps and thirty-day retention independent of receipt settlement. Latest outcome, failure and replay evidence is prioritized. Summaries describe gaps, not an exhaustive historical audit. IDs remain immutable and cursors remain valid after pruning. Summaries are current observations across the identity, independent of the detail page cursor.
 */
export type EventFanoutAttemptHistoryResponse = {
  app_slug: string;
  event_source: string;
  event_id: string;
  subscription_id?: string;
  history: Array<EventFanoutAttemptResponse>;
  /**
   * Opaque cursor bound to the app, event identity, and optional recipient filter.
   */
  next_before?: string;
  /**
   * Recorded observations only; pre-migration transitions and compacted details are unavailable.
   */
  coverage?: 'bounded_recorded_outcomes';
  summaries?: Array<EventFanoutHistorySummaryResponse>;
};

