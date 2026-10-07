/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventReplayPreviewMatch } from './EventReplayPreviewMatch.js';
import type { EventReplayPreviewRetention } from './EventReplayPreviewRetention.js';
import type { EventSubscriptionResponse } from './EventSubscriptionResponse.js';
/**
 * Read-only current-subscription preview of one bounded retained-event page.
 */
export type EventReplayPreviewResponse = {
  app_slug: string;
  subscription: EventSubscriptionResponse;
  /**
   * Opaque fingerprint of the current subscription declaration.
   */
  subscription_revision: string;
  from: string;
  until: string;
  /**
   * Fixed exclusive acceptance cutoff anchored on the first page.
   */
  cutoff_at: string;
  /**
   * Observation time for this page.
   */
  observed_at: string;
  coverage: 'retained_envelopes';
  retention: EventReplayPreviewRetention;
  /**
   * Envelopes examined on this page.
   */
  scanned_count: number;
  matched_count: number;
  filter_mismatch_count: number;
  pattern_mismatch_count: number;
  /**
   * Matching events whose original snapshot contains this target.
   */
  already_captured_count: number;
  matches: Array<EventReplayPreviewMatch>;
  /**
   * Continue even if this page has no matches; absent when no more currently retained candidates follow.
   */
  next_after?: string;
};

