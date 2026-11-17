/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Complete/missing/stale/conflicting/unavailable partition target observations. Worker states, waiting queues and saturated slot allocators overlap that partition and retain independently usable component evidence. These are process-local observations, not fleet capacity or successful builds.
 */
export type ObsRuntimeBuilderCounts = {
  expected: number;
  observed: number;
  complete: number;
  missing: number;
  stale: number;
  conflicting: number;
  unavailable: number;
  unexpected: number;
  overdue_workers: number;
  waiting_workers: number;
  working_workers: number;
  draining_workers: number;
  waiting_queues: number;
  saturated_slots: number;
};

