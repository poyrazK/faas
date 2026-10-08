/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Acceptance-time range for a durable subscription event backfill.
 */
export type EventReplayBackfillRequest = {
  /**
   * Explicitly override delivery age for this replay generation or historical backfill job. Preserves deterministic invocation identity and manual controls.
   */
  allow_expired?: boolean;
  /**
   * Inclusive platform acceptance-time lower bound.
   */
  from: string;
  /**
   * Exclusive upper bound; future values are capped at creation time. The range may not exceed 30 days.
   */
  until: string;
};

