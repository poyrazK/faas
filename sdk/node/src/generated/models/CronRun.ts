/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One execution of a cron: when it fired, how long it ran, and how it ended. HTTP fires project invocations; command fires project deployment-attached app tasks.
 */
export type CronRun = {
  /**
   * Invocation id for HTTP crons or task UUID for command crons.
   */
  id: string;
  /**
   * When the cron fired (the invocation/task creation time), not when the app began executing.
   */
  started_at: string;
  /**
   * When the run reached a terminal state; null while still in flight.
   */
  completed_at?: string | null;
  /**
   * completed_at - started_at in milliseconds, computed server-side. Null while the run is still in flight.
   */
  duration_ms?: number | null;
  /**
   * Normalized result. `timeout` means the dispatch exceeded its deadline; `dead_letter` means the retry budget was exhausted; `running` means the run has not reached a terminal state yet. Branch on this, never on `error`.
   */
  outcome: 'success' | 'failed' | 'timeout' | 'dead_letter' | 'running' | 'cancelled';
  /**
   * Dispatch attempts for this run; greater than 1 means it was retried.
   */
  attempts: number;
  /**
   * The instance that served the run; null if the fire never reached one.
   */
  instance_id?: string | null;
  /**
   * Deployment-attached app-task receipt for command crons.
   */
  task_id?: string;
  /**
   * Operator-facing failure text. Unstructured and unversioned — do not parse it.
   */
  error?: string | null;
};

