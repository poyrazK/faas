/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryExecutionSummary } from './EventRecoveryExecutionSummary.js';
/**
 * Frozen confirmed terminal execution results for all queued deliveries after admission finishes. Unknown outcomes block capture. Separate from event_recovery.completed; does not imply every execution succeeded or any receiver acknowledged.
 */
export type EventRecoveryExecutionFinishedWebhookPayload = {
  event_id: string;
  job_id: string;
  app_id: string;
  mode: 'execution';
  state: 'completed' | 'cancelled';
  outcome: 'all_succeeded' | 'finished_with_non_success';
  selected_count: number;
  pending_count: 0;
  queued_count: number;
  skipped_count: number;
  cancelled_count: number;
  created_at: string;
  expires_at: string;
  completed_at: string;
  execution: EventRecoveryExecutionSummary;
  execution_finished_at: string;
  /**
   * Unknown or nonterminal queued deliveries; capture requires zero.
   */
  unresolved_count: 0;
};

