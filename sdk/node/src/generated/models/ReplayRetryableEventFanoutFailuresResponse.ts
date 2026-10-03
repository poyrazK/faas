/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Summary of one bounded app-scoped replay request.
 */
export type ReplayRetryableEventFanoutFailuresResponse = {
  app_slug: string;
  replayed_count: number;
  /**
   * True when more retryable failures remain for a later request.
   */
  has_more: boolean;
};

