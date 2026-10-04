/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Versioned recurring-work scheduling policy for Jobs and both HTTP and command Crons. HTTP replace waits for a prior dispatched request to complete because the scheduler has no stop acknowledgement for a request already delivered to the app.
 */
export type SchedulePolicy = {
  version: 1;
  /**
   * Allow concurrent occurrences, record-and-skip while one is active, or replace when previous work can be stopped safely. HTTP Crons wait for a dispatched request to finish.
   */
  overlap: 'allow' | 'skip' | 'replace';
  /**
   * Maximum delay from the nominal schedule time to the first task start; zero or omitted disables the deadline.
   */
  start_deadline_seconds?: number;
  /**
   * On scheduler recovery, coalesce the due backlog into its latest occurrence or record stale occurrences as skipped.
   */
  missed_runs: 'coalesce_latest' | 'skip';
};

