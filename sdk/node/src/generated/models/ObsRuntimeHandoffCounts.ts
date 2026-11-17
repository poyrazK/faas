/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Complete/missing/stale/conflicting/unavailable partition inspected targets. Backlog and recovery counts overlap that partition and count observation scopes, not unique builds. Unsuccessful collections count fresh zero success reports, including observers without any successful read yet.
 */
export type ObsRuntimeHandoffCounts = {
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
  unsuccessful_collections: number;
  working_recovery_scopes: number;
  waiting_recovery_scopes: number;
  stopping_recovery_scopes: number;
  not_started_recovery_scopes: number;
};

