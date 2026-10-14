/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Retained diagnostic scan status, durable cursor, and repair counters for one subscription.
 */
export type EventExecutionLineageCaseBackfill = {
  request_id?: string;
  previous_request_id?: string;
  subscription_id: string;
  app_id: string;
  status: 'not_requested' | 'pending' | 'waiting_for_retry_jobs' | 'paused' | 'cancelled' | 'completed' | 'expired';
  through_outbox_id: number;
  cursor: number;
  scanned: number;
  recorded: number;
  existing: number;
  associated: number;
  unresolved: number;
  conflicts: number;
  created_at?: string;
  updated_at?: string;
  /**
   * Work deadline; latest scan metadata remains available while its coverage row exists.
   */
  retain_until?: string;
};

