/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Complete/missing/stale/conflicting/unavailable partition targets. Remaining counts overlap that partition. Complete means usable components; historical ages may still be unknown. Waiting scopes have any observed pending phase; empty scopes have all three phases known empty. Disabled observation never implies drained work.
 */
export type ObsRuntimePreparationCounts = {
  expected: number;
  observed: number;
  complete: number;
  missing: number;
  stale: number;
  conflicting: number;
  unavailable: number;
  unexpected: number;
  waiting_scopes: number;
  empty_scopes: number;
  unknown_age_scopes: number;
  unsuccessful_collections: number;
  enabled_scopes: number;
  disabled_scopes: number;
};

