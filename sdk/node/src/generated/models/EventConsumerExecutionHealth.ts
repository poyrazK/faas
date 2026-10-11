/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Current retained execution states for admitted application event deliveries and retained handler replays. Attempts and successful completion latency use the requested window. Counts are bounded observations, include backfills, and are not unique event counts. Version filtering and routing failures are excluded. Historical coverage is always incomplete.
 */
export type EventConsumerExecutionHealth = {
  subscription_id: string;
  app_id: string;
  observed_at: string;
  window_start: string;
  coverage: 'bounded_retained_execution_roots';
  /**
   * Receipt, invocation, and attempt retention make this an incomplete historical observation.
   */
  history_complete: boolean;
  /**
   * More than 1000 retained delivery roots or 5000 linked invocations existed.
   */
  truncated: boolean;
  retained_roots: number;
  missing_roots: number;
  executions: number;
  queued: number;
  running: number;
  retrying: number;
  succeeded: number;
  failed: number;
  expired: number;
  dead_lettered: number;
  cancelled: number;
  superseded: number;
  unknown: number;
  successful_attempts: number;
  failed_attempts: number;
  unknown_attempts: number;
  window_completions: number;
  window_dead_letters: number;
  handler_failure_pct: number;
  dead_letter_rate_per_second: number;
  completion_latency_p95_seconds: number;
};

