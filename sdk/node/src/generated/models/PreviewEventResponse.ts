/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventPreviewSubscription } from './EventPreviewSubscription.js';
/**
 * Read-only event-routing preview; counts cover all candidates while lists are bounded samples.
 */
export type PreviewEventResponse = {
  event_id: string;
  source: string;
  type: string;
  /**
   * Enabled source/type candidates considered.
   */
  candidate_count: number;
  /**
   * Subscriptions that would receive the event.
   */
  matched_count: number;
  /**
   * Candidates excluded by their content filters.
   */
  filter_mismatch_count: number;
  /**
   * Candidates rejected for another reason, including invalid stored subscription data.
   */
  other_mismatch_count: number;
  matches: Array<EventPreviewSubscription>;
  non_matches: Array<EventPreviewSubscription>;
  /**
   * True when either sample omits additional candidates.
   */
  truncated: boolean;
};

