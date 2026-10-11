/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationHealthRun } from './AutomationHealthRun.js';
import type { AutomationHealthStepFailure } from './AutomationHealthStepFailure.js';
import type { AutomationQueueHealth } from './AutomationQueueHealth.js';
/**
 * Bounded aggregate automation health. Customer payloads and error strings are never returned.
 */
export type AutomationHealthResponse = {
  app_slug: string;
  automation_name: string;
  window_start: string;
  window_end: string;
  run_count: number;
  completed_run_count: number;
  /**
   * Current non-terminal runs that have started, including retries and parked waits; independent of the requested health window.
   */
  active_run_count: number;
  /**
   * Current pending runs that have not started; independent of the requested health window.
   */
  queued_run_count: number;
  queue?: AutomationQueueHealth;
  /**
   * Succeeded runs divided by succeeded, failed and dead runs; zero when none completed.
   */
  success_rate: number;
  status_counts: {
    pending: number;
    running: number;
    awaiting_event: number;
    succeeded: number;
    failed: number;
    dead: number;
  };
  /**
   * Median duration of completed runs with start and finish timestamps; omitted when no samples exist.
   */
  p50_duration_ms?: number;
  /**
   * 95th percentile duration of completed runs with start and finish timestamps; omitted when no samples exist.
   */
  p95_duration_ms?: number;
  last_run?: AutomationHealthRun;
  last_success?: AutomationHealthRun;
  last_failure?: AutomationHealthRun;
  failed_steps: Array<AutomationHealthStepFailure>;
};

