/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryAutomaticAction } from './WorkflowRecoveryAutomaticAction.js';
import type { WorkflowRecoveryInitialQueueEvidence } from './WorkflowRecoveryInitialQueueEvidence.js';
import type { WorkflowRecoveryQueueEvidence } from './WorkflowRecoveryQueueEvidence.js';
/**
 * One pending or unresolved applied automatic retry. Queued_at is the frozen review preparation time. Ages are whole nonnegative seconds relative to the queue observed_at. Waiting age is omitted until the worker records a wait; the saved reason may be historical.
 */
export type WorkflowRecoveryAutomaticQueueEntry = {
  initial_evidence?: WorkflowRecoveryInitialQueueEvidence;
  evidence?: WorkflowRecoveryQueueEvidence;
  evidence_unavailable_reason?: 'review_not_retained' | 'plan_changed';
  wait_category?: 'worker' | 'policy' | 'budget' | 'cooldown' | 'evidence' | 'execution';
  /**
   * Current pending policy pause/disable reason or the last saved worker reason; evidence diagnosis has its own observation time.
   */
  wait_reason?: string;
  review_id: string;
  policy_version: number;
  queued_at: string;
  queued_age_seconds: number;
  waiting_age_seconds?: number;
  action: WorkflowRecoveryAutomaticAction;
};

