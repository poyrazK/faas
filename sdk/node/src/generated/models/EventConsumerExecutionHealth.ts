/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Current retained execution states for admitted application event deliveries and retained handler replays. Attempts and successful completion latency use the requested window. Counts are bounded observations, include backfills, and are not unique event counts. Version filtering and routing failures are excluded. History completeness is established only for fully covered windows of newly instrumented subscriptions; legacy baselines remain unknown and evidence loss is partial.
 */
export type EventConsumerExecutionHealth = {
  /**
   * Latest valid successful completion among the bounded retained executions; absent when no such completion is retained. Not an all-time watermark and not restricted to the attempt window.
   */
  latest_successful_completion_at?: string;
  subscription_id: string;
  app_id: string;
  observed_at: string;
  window_start: string;
  coverage: 'bounded_retained_execution_roots';
  /**
   * True only for an instrumented, fully covered window with intact evidence and no truncation or unknown outcomes.
   */
  history_complete: boolean;
  history_status?: 'complete' | 'partial' | 'unknown';
  /**
   * Stable explanation of coverage eligibility or evidence loss.
   */
  history_reason?: string;
  /**
   * Durable collection start; absent when no coverage record exists.
   */
  coverage_started_at?: string;
  /**
   * First persisted evidence loss; loss is not automatically cleared.
   */
  coverage_lost_at?: string;
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

