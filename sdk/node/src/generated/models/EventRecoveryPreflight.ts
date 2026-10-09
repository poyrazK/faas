/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryPreflightItem } from './EventRecoveryPreflightItem.js';
/**
 * Read-only pending-item eligibility, current capacity and optimistic drain estimate for a recovery job.
 */
export type EventRecoveryPreflight = {
  /**
   * Pending items with unheld receipt deadlines at or before the later of 24 hours from observation and optimistic drain.
   */
  receipt_retention_warning_count?: number;
  /**
   * Pending items currently pinned by a backfill.
   */
  receipt_retention_held_count?: number;
  earliest_unheld_retain_until?: string;
  /**
   * Rate-only optimistic drain reaches or exceeds the earliest currently unheld receipt boundary; false is not a retention guarantee.
   */
  minimum_drain_crosses_receipt_retention?: boolean;
  job_id: string;
  observed_at: string;
  state: 'running' | 'paused' | 'completed' | 'cancelled';
  /**
   * Running or paused and not expired at the observation time.
   */
  active: boolean;
  pending_count: number;
  eligible_count: number;
  waiting_count: number;
  likely_skipped_count: number;
  unknown_count: number;
  reason_counts: Record<string, number>;
  capacity_scopes: Record<string, number>;
  rate_per_second: number;
  remaining_lifetime_seconds: number;
  /**
   * Optimistic delay until the last pending item can spend an admission permit, respecting the current fixed-window budget and schedule. Does not include handler execution or future waits.
   */
  minimum_drain_seconds: number;
  earliest_drain_at: string;
  /**
   * Active job and optimistic earliest drain strictly precedes expiry; true is not a guarantee of completion.
   */
  fits_before_expiry: boolean;
  assumes_immediate_resume: boolean;
  sample: Array<EventRecoveryPreflightItem>;
};

