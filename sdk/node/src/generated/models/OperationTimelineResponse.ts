/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationTimelineEntry } from './OperationTimelineEntry.js';
/**
 * Chronological page merged from operation events, attempts and completion deliveries.
 */
export type OperationTimelineResponse = {
  operation_id: string;
  state: 'accepted' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'requires_reconciliation';
  generation: number;
  entries: Array<OperationTimelineEntry>;
  /**
   * Event history was pruned before the operation; attempts and deliveries remain.
   */
  events_expired?: boolean;
  next_cursor?: string;
};

