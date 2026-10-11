/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryNotificationRetryTarget } from './EventRecoveryNotificationRetryTarget.js';
/**
 * Immutable original receiver decision alongside current retained delivery status. An unavailable current row does not rewrite or invalidate the original result.
 */
export type EventRecoveryNotificationRetryDecision = {
  target: EventRecoveryNotificationRetryTarget;
  state: 'queued' | 'skipped';
  reason?: string;
  replay_generation?: number;
  current_delivery_status: string;
  current_replay_generation?: number;
  /**
   * Outcome of the originally queued generation. Retained terminal attempts prove success or failure; matching active delivery proves pending. Missing evidence is unknown and skipped decisions are not applicable.
   */
  retry_outcome: 'not_applicable' | 'pending' | 'succeeded' | 'failed' | 'unknown';
  /**
   * Number of retained completed attempts for the originally queued generation, excluding other generations and in-flight attempts.
   */
  retained_attempt_count: number;
  /**
   * True when retained attempt numbers form a complete sequence through the known outcome; unknown outcomes always report false. Not applicable decisions have a complete zero count.
   */
  attempt_count_complete: boolean;
  /**
   * Finish time of the retained terminal attempt for the originally queued generation, present only for succeeded or failed outcomes.
   */
  completed_at?: string;
};

