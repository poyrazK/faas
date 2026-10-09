/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Select routing failures (default) or the latest replayable retained execution per application event consumer. Execution mode includes publication and materialized backfill recipients, excludes workflows and object notifications, and requires retained admission and execution records. Creation freezes its own selection; preview is advisory.
 */
export type EventRecoveryRequest = {
  /**
   * Optional operator reason, limited to 512 UTF-8 bytes without control characters. Stored only in audit history; omitted from frozen selection. Preview does not record it.
   */
  reason?: string;
  mode?: 'routing' | 'execution';
  /**
   * Execution mode only; omitted selects both replayable outcomes.
   */
  outcome?: 'failed' | 'dead_letter';
  subscription_id?: string;
  event_source?: string;
  event_type?: string;
  failure_code?: string;
  /**
   * Minimum age of the recorded terminal failure.
   */
  min_age_seconds?: number;
  include_non_retryable?: boolean;
  /**
   * Maximum recipients processed per job in a one-second window; zero uses the default. Actual throughput depends on scheduler load.
   */
  rate_per_second?: number;
};

