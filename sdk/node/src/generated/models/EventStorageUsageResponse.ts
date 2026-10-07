/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Retained customer event counts, logical JSON bytes, pending age and current account budgets.
 */
export type EventStorageUsageResponse = {
  retained_events: number;
  retained_bytes: number;
  pending_events: number;
  oldest_pending_at: string | null;
  limits: {
    retained_events: number;
    retained_bytes: number;
  };
};

