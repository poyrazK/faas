/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Select routing failures (default) or the latest replayable retained execution per application event consumer. With parent_job_id, saved parent failures remain selectable even when execution or receipt evidence has disappeared; admission skips changes rather than following newer work. Ordinary execution mode includes publication and materialized backfill recipients, excludes workflows and object notifications, and requires retained admission and execution records. Creation freezes its own selection; preview is advisory.
 */
export type EventRecoveryRequest = {
  /**
   * Select only saved failed/dead-lettered queued items from this retained terminal execution recovery in the same account/app. Requires execution mode. Does not follow newer replays.
   */
  parent_job_id?: string;
  /**
   * Required on child creation; optional on preview. Account-scoped durable idempotency while the child job is retained. Repeating the normalized selection returns its existing child; different selection/app with the same UUID conflicts. Only allowed with parent_job_id. Operator reason is excluded from comparison and the original audit reason wins.
   */
  request_id?: string;
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
  /**
   * Hold selected retained receipts from pruning while their items are pending and the job is active and unexpired. Pausing does not extend the existing 24-hour lifetime. Preview acquires no holds. Held receipts still count toward account storage limits.
   */
  protect_receipts?: boolean;
  include_non_retryable?: boolean;
  /**
   * Maximum recipients processed per job in a one-second window; zero uses the default. Actual throughput depends on scheduler load.
   */
  rate_per_second?: number;
};

