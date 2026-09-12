/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One observed scheduler reason an application remained resident. The
 * platform reports evidence available at the scheduler boundary; it
 * does not infer a protocol or predict a cost saving.
 *
 */
export type DebugRunningCause = {
  code: 'request_activity' | 'open_connection' | 'tail_tasks' | 'min_instances' | 'prewarm_floor' | 'scale_in_cooldown' | 'workload_mode' | 'startup_grace' | 'activity_unknown' | 'no_blocker_observed';
  summary: string;
  instance_count: number;
  open_connections?: number;
  tail_tasks?: number;
  mode?: string;
  workload_class?: string;
  last_activity_at?: string;
  idle_deadline?: string;
};

