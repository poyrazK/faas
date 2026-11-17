/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Full-set scheduler-scope coverage, including overlapping scopes. Nonempty requires any observed schedule, including disabled or not-due intent; empty requires all ten classes observed empty. Due scopes contain any first-five class schedules; inactive scopes contain disabled, suspended, account-held or not-due schedules. Those categories can overlap. Sweep categories describe observed scan activity, not command delivery.
 */
export type ObsRuntimeCommandCronCounts = {
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
  due_scopes: number;
  inactive_scopes: number;
  sweep_running_scopes: number;
  sweep_idle_scopes: number;
  sweep_never_started_scopes: number;
  sweep_never_succeeded_scopes: number;
};

