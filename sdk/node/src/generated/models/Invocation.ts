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
  source: 'async_invoke' | 'queue' | 'delayed_task' | 'cron';
  state: 'pending' | 'dispatching' | 'completed' | 'failed' | 'cancelled';
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
};

