/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Replica-view coverage and task evidence across the full inspected set before clipping. Nonempty means any positive task class; empty requires all 27 kind/class observations empty. Observer expectation and local dispatcher activity are independent. Running and stopped runner views partition dispatch-enabled views; occupied and saturated views describe local handler slots.
 */
export type ObsRuntimeAppTaskCounts = {
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
  dispatch_enabled_views: number;
  dispatch_disabled_views: number;
  runner_running_views: number;
  runner_stopped_views: number;
  occupied_views: number;
  saturated_views: number;
};

