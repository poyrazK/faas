/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowInitialEvidenceRepairReceipt } from './WorkflowInitialEvidenceRepairReceipt.js';
import type { WorkflowInitialFailure } from './WorkflowInitialFailure.js';
import type { WorkflowInitialObservationFailure } from './WorkflowInitialObservationFailure.js';
import type { WorkflowInitialRecoveryAttention } from './WorkflowInitialRecoveryAttention.js';
import type { WorkflowInitialRecoveryReviewEvent } from './WorkflowInitialRecoveryReviewEvent.js';
import type { WorkflowRecoveryAutomaticBlockedNotification } from './WorkflowRecoveryAutomaticBlockedNotification.js';
import type { WorkflowRecoveryAutomaticEvaluation } from './WorkflowRecoveryAutomaticEvaluation.js';
import type { WorkflowRecoveryPolicy } from './WorkflowRecoveryPolicy.js';
import type { WorkflowRecoveryPollingNotification } from './WorkflowRecoveryPollingNotification.js';
import type { WorkflowRecoveryReviewActionStatus } from './WorkflowRecoveryReviewActionStatus.js';
import type { WorkflowRecoveryReviewNotification } from './WorkflowRecoveryReviewNotification.js';
import type { WorkflowRunRecoveryTarget } from './WorkflowRunRecoveryTarget.js';
/**
 * Immutable failure and policy snapshot with durable transition history. Pending reviews expire after 24 hours. Fresh read status is advisory; submission and pending native admission repeat checks. Preparation and preview-mode manual submission each enforce the policy cooldown and hourly action budget; preparation shares the existing parent-review budget. Retained for 30 days with at most 1000 initial reviews per app; unresolved automatic submissions remain retained until proven final. automatic_initial submission reserves the separate hourly execution budget and one unresolved recovery slot per app. Automatic outcomes require saved terminal generation evidence or final admission refusal. Submitted job completion and notifications use the native recovery job lifecycle.
 */
export type WorkflowInitialRecoveryReview = {
  polling_notification?: WorkflowRecoveryPollingNotification;
  evidence_repair?: WorkflowInitialEvidenceRepairReceipt;
  automatic_block_reason?: string;
  automatic_block_sequence?: number;
  automatic_block_notification?: WorkflowRecoveryAutomaticBlockedNotification;
  observation_failure?: WorkflowInitialObservationFailure;
  attention?: WorkflowInitialRecoveryAttention;
  /**
   * Verified completion of a manually approved initial recovery.
   */
  result?: WorkflowRecoveryReviewActionStatus;
  automatic_result?: WorkflowRecoveryReviewActionStatus;
  automatic_evaluation?: WorkflowRecoveryAutomaticEvaluation;
  id: string;
  app_id: string;
  app: string;
  failure: WorkflowInitialFailure;
  target: WorkflowRunRecoveryTarget;
  policy_version: number;
  policy: WorkflowRecoveryPolicy;
  status: 'prepared' | 'submitted' | 'cancelled' | 'expired' | 'stale';
  reason: string;
  prepared_at: string;
  expires_at: string;
  observed_at: string;
  job_id?: string;
  submitted_at?: string;
  notification?: WorkflowRecoveryReviewNotification;
  events: Array<WorkflowInitialRecoveryReviewEvent>;
};

