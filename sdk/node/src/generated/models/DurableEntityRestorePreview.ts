/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Read-only comparison of candidate and current state; compatibility remains unverified.
 */
export type DurableEntityRestorePreview = {
  current_version: number;
  source_version: number;
  expected_version_matches: boolean;
  current_schema_version?: number;
  source_schema_version?: number;
  schema_relation: 'same' | 'older' | 'newer' | 'unknown';
  compatibility: 'unverified';
  alarm_pending: boolean;
  outbox_pending: number;
  alarm_exhausted: boolean;
  outbox_exhausted: boolean;
};

