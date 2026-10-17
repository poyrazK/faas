/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventAcknowledgmentOutcome } from './EventAcknowledgmentOutcome.js';
/**
 * Bounded page of coalesced outcomes ordered by immutable group identity.
 */
export type EventAcknowledgmentHistoryPage = {
  /**
   * Groups ordered by first_at, invocation UUID, attempt, generation and kind descending.
   */
  outcomes: Array<EventAcknowledgmentOutcome>;
  /**
   * Another retained matching group existed in this read.
   */
  has_more: boolean;
  /**
   * Continuation emitted only when has_more is true.
   */
  next_cursor?: string;
  /**
   * Fixed initial read time excluding groups with later first occurrences.
   */
  cutoff: string;
  /**
   * Retained diagnostic groups with live aggregate values and no historical snapshot guarantee.
   */
  coverage: 'retained_coalesced_deferred_outcomes';
};

