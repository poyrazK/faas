/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Complete/missing/stale/conflicting/unavailable partition replica observations. Remaining counts overlap that partition and count views, never builds or obligations. Running and cleanup views may overlap; nonempty is their union, empty requires both components observed empty, and retried cleanup views are a subset of cleanup views.
 */
export type ObsRuntimeBuildRecoveryCounts = {
  expected: number;
  observed: number;
  complete: number;
  missing: number;
  stale: number;
  conflicting: number;
  unavailable: number;
  unexpected: number;
  nonempty_views: number;
  empty_views: number;
  running_views: number;
  cleanup_views: number;
  retried_cleanup_views: number;
  unsuccessful_collections: number;
};

