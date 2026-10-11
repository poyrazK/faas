/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRetentionSample } from './EventRetentionSample.js';
import type { EventStorageUsageResponse } from './EventStorageUsageResponse.js';
/**
 * Filtered receipt observations and unfiltered account-wide retained storage utilization.
 */
export type EventRetentionHealth = {
  observed_at: string;
  window_seconds: number;
  event_source?: string;
  app_id?: string;
  retained_receipts: number;
  retained_bytes: number;
  /**
   * Settled receipts missing a settlement timestamp; expiry cannot be determined.
   */
  unknown_deadline_receipts: number;
  unsettled_receipts: number;
  held_receipts: number;
  /**
   * Settled receipts primarily held by opted-in active recovery jobs with pending items. Backfill holds take precedence.
   */
  recovery_holds: number;
  running_backfill_holds: number;
  retryable_backfill_holds: number;
  held_due_receipts: number;
  eligible_for_pruning: number;
  expiring_receipts: number;
  storage: EventStorageUsageResponse;
  storage_count_utilization_pct: number;
  storage_bytes_utilization_pct: number;
  storage_utilization_pct: number;
  sample: Array<EventRetentionSample>;
  sample_truncated: boolean;
};

