/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Current observations of admitted execution-mode items, including legacy admissions as unknown. Counts sum to tracked_count and are separate from job admission state. Omitted for routing recovery. Retention can turn a previously known outcome into unknown.
 */
export type EventRecoveryExecutionSummary = {
  observed_at: string;
  tracked_count: number;
  queued: number;
  running: number;
  retrying: number;
  succeeded: number;
  failed: number;
  dead_lettered: number;
  expired: number;
  cancelled: number;
  superseded: number;
  unknown: number;
};

