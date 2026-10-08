/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Principal read position with unread count and current retention bounds.
 */
export type ManagedRealtimeReadProgressResponse = {
  sequence: number;
  unread: number;
  oldest_sequence: number;
  latest_sequence: number;
  history_unavailable: boolean;
  updated_at?: string;
};

