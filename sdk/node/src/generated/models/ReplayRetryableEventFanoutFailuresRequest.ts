/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Bounded app-scoped replay of failures classified as retryable.
 */
export type ReplayRetryableEventFanoutFailuresRequest = {
  /**
   * Optional exact event source filter; must be paired with event_id.
   */
  event_source?: string;
  /**
   * Optional exact event ID filter; must be paired with event_source.
   */
  event_id?: string;
  /**
   * Maximum recipients to requeue in this call.
   */
  limit?: number;
};

