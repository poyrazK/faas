/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Original retry request counts and decision time with current outcomes for its queued generations, observed at the history response timestamp.
 */
export type EventRecoveryNotificationRetryDecisionSummary = {
  request_id: string;
  decided_at: string;
  target_count: number;
  queued_count: number;
  skipped_count: number;
  succeeded_count: number;
  failed_count: number;
  pending_count: number;
  unknown_count: number;
  /**
   * Aggregate outcome for original queued generations. Known failure takes precedence, then unknown evidence or no queued targets, then pending; success requires every queued target succeeded.
   */
  status: 'succeeded' | 'failed' | 'pending' | 'inconclusive';
  /**
   * True when every queued target has a known outcome and complete retained attempt sequence. All-skipped requests have complete empty evidence but are inconclusive.
   */
  evidence_complete: boolean;
  /**
   * Latest terminal attempt finish time, present only when every queued target has a retained terminal outcome and at least one target was queued.
   */
  completed_at?: string;
};

