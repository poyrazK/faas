/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryExecution } from './EventRecoveryExecution.js';
/**
 * Metadata-only selected recipient. In execution mode invocation_id identifies the selected failure. Queued means replay was admitted, not that the handler succeeded.
 */
export type EventRecoveryItem = {
  /**
   * Parent recovery job identity for a child selection; retained as historical lineage even if the parent job is pruned.
   */
  parent_job_id?: string;
  /**
   * Selected item position in the parent recovery.
   */
  parent_position?: number;
  invocation_id?: string;
  /**
   * Exact replay admitted by this job; omitted for routing recovery and legacy items.
   */
  replay_invocation_id?: string;
  /**
   * Frozen admitted generation including zero for new replay children.
   */
  replay_generation?: number;
  execution?: EventRecoveryExecution;
  position: number;
  event_source: string;
  event_id: string;
  event_type: string;
  subscription_id: string;
  failed_at: string;
  failure_code: string;
  retryable: boolean;
  state: 'pending' | 'queued' | 'skipped' | 'cancelled';
  reason?: 'changed' | 'receipt_expired' | 'target_unavailable' | 'cancelled' | 'expired';
};

