/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Complete/missing/stale/conflicting/unavailable partition inspected scheduler targets. Other counts overlap this partition and describe observation scopes. Nonempty scopes have any current attempts, including active or snapshot-recorded classes; empty requires all six classes known empty. Full component availability can include unknown ages.
 */
export type ObsRuntimeSnapshotPrimeCounts = {
  expected: number;
  observed: number;
  complete: number;
  missing: number;
  stale: number;
  conflicting: number;
  unavailable: number;
  unexpected: number;
  nonempty_scopes: number;
  empty_scopes: number;
  unknown_age_scopes: number;
  unsuccessful_collections: number;
  enabled_scopes: number;
  disabled_scopes: number;
};

