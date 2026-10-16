/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A usable value requires healthy evidence; null distinguishes missing or stale evidence from a fresh zero. Observed time comes from the oldest underlying last-observed sample across matching series.
 */
export type ObsNotificationSignal = {
  state: 'healthy' | 'no_samples' | 'stale' | 'unavailable';
  reason?: string;
  value: number | null;
  observed_at?: string;
  window_seconds: 0 | 600;
  max_age_seconds: number;
};

