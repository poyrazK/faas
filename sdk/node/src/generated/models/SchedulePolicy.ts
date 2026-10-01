/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Versioned recurring-work scheduling policy.
 */
export type SchedulePolicy = {
  version: 1;
  /**
   * Allow concurrent occurrences, record-and-skip while one is active, or stop prior scheduled work before replacement.
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

