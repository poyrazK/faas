/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WakeTimelineEvent } from './WakeTimelineEvent.js';
import type { WakeTimelinePage } from './WakeTimelinePage.js';
/**
 * Envelope for `GET /v1/apps/{slug}/wakes/{wake_id}/timeline`.
 */
export type WakeTimelineResponse = {
  /**
   * Echo of the path-segment wake_id.
   */
  wake_id: string;
  /**
   * Resolved app id (the slug's owning app).
   */
  app_id: string;
  events: Array<WakeTimelineEvent>;
  /**
   * Legacy timestamp hint, sent back as since. Can skip equal-time events. keyset_v1 uses page.next_cursor instead.
   */
  next_cursor?: string;
  /**
   * Effective limit applied (always 1..1000).
   */
  limit: number;
  page?: WakeTimelinePage;
};

