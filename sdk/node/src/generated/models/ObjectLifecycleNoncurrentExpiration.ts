/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Expire noncurrent versions after their successor age and optional retained-version count both qualify.
 */
export type ObjectLifecycleNoncurrentExpiration = {
  noncurrent_days: number;
  newer_noncurrent_versions?: number;
};

