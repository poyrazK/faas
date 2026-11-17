/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Complete/missing/stale/conflicting/unavailable partition replica observations. Other counts overlap the partition and count replica views, not deployments. Nonempty means any reported durable class evidence; empty requires all eight classes observed empty. Complete component evidence can contain unknown ages. Conflicting means inconsistent source series, not disagreement between replicas.
 */
export type ObsRuntimePostCaptureCounts = {
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
  unknown_age_views: number;
  unsuccessful_collections: number;
  enabled_views: number;
  disabled_views: number;
};

