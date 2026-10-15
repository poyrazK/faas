/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventExecutionLineageRetryJobControlReceipt } from './EventExecutionLineageRetryJobControlReceipt.js';
import type { EventExecutionLineageRetryJobItem } from './EventExecutionLineageRetryJobItem.js';
/**
 * Durable bulk lineage retry status, original selection, per-case results, and optional command receipt.
 */
export type EventExecutionLineageRetryJob = {
  /**
   * Last committed evaluation or skip recorded by upgraded workers. Absent before the first evaluation or for older snapshots; controls do not change it.
   */
  last_progress_at?: string;
  /**
   * Immutable command outcome accompanying a UUID-keyed control response; outer job fields are current observations.
   */
  control_receipt?: EventExecutionLineageRetryJobControlReceipt;
  request_id: string;
  subscription_id: string;
  app_id: string;
  status: 'pending' | 'paused' | 'completed' | 'cancelled';
  /**
   * Cases evaluated or skipped before cancellation; cancelled items are not counted.
   */
  processed: number;
  items: Array<EventExecutionLineageRetryJobItem>;
  created_at: string;
  updated_at: string;
  retain_until: string;
};

