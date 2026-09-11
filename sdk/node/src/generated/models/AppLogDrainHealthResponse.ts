/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable, customer-safe delivery health for one runtime log destination.
 */
export type AppLogDrainHealthResponse = {
  log_drain_id: string;
  status: 'unknown' | 'healthy' | 'degraded' | 'inactive';
  active: boolean;
  queue_depth: number;
  queue_capacity: number;
  delivered_total: number;
  failed_total: number;
  dropped_total: number;
  retries_total: number;
  stream_reconnects_total: number;
  gaps_total: number;
  last_success_at?: string;
  last_failure_at?: string;
  /**
   * Sanitized delivery summary; never raw transport output.
   */
  last_error?: string;
  updated_at: string;
};

