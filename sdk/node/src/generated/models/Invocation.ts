/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A single invocations row. Account-scoped; cross-account reads return 404.
 */
export type Invocation = {
  id: string;
  app_id: string;
  account_id: string;
  source: 'async_invoke' | 'queue' | 'delayed_task' | 'cron' | 'replay';
  state: 'pending' | 'dispatching' | 'completed' | 'failed' | 'cancelled' | 'dead_letter';
  method?: string;
  path?: string;
  payload?: Record<string, any>;
  headers?: Record<string, any>;
  scheduled_at?: string | null;
  due_at?: string;
  created_at: string;
  completed_at?: string | null;
  instance_id?: string | null;
  result?: any | null;
  last_error?: string | null;
  /**
   * Optional ack URL for queueReceive consumers; populated on queue-sourced rows.
   */
  ack_url?: string | null;
  /**
   * Number of dispatch attempts so far; 0 on the first try.
   */
  attempts?: number;
  /**
   * When the in-flight dispatch lease expires; null when no lease is held.
   */
  lease_expires_at?: string | null;
  /**
   * When the drain first claimed the row; null until claimed.
   */
  received_at?: string | null;
  /**
   * ADR-134 PR-B. Optional hard-stop. Drain transitions the row to dead_letter when this time passes while still pending|dispatching.
   */
  deadline_at?: string | null;
  /**
   * ADR-134 PR-B. Optional per-row retry curve override; decodes into dispatch.RetryPolicy (max_attempts, base_seconds, max_seconds, jitter_seconds).
   */
  retry_policy?: any | null;
  /**
   * ADR-134 PR-B. Optional explicit retention horizon. NULL means 'use plan default' (Limits.MaxAsyncResultRetentionSeconds).
   */
  result_retention_until?: string | null;
  /**
   * ADR-134 PR-C. When this row was most recently replayed from dead_letter via POST /v1/apps/{slug}/queues/dead_letter/{id}/replay. NULL until first replay.
   */
  last_replayed_at?: string | null;
};

