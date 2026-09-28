/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * App work policy settings; durations use whole milliseconds.
 */
export type UpsertWorkPolicyRequest = {
  max_running_per_key: 1;
  pending_updates?: 'all' | 'keep_latest';
  debounce_ms?: number;
  expires_after_ms?: number;
};

