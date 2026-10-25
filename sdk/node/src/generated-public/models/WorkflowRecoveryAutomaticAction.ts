/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryAutomaticBlockedNotification } from './WorkflowRecoveryAutomaticBlockedNotification.js';
import type { WorkflowRecoveryAutomaticEvaluation } from './WorkflowRecoveryAutomaticEvaluation.js';
import type { WorkflowRecoveryAutomaticSuppression } from './WorkflowRecoveryAutomaticSuppression.js';
import type { WorkflowRecoveryReviewActionStatus } from './WorkflowRecoveryReviewActionStatus.js';
/**
 * Durable automatic retry intent and application receipt. Applied identifies child creation; its final result requires retained generation proof. Suppressed actions never run automatically.
 */
export type WorkflowRecoveryAutomaticAction = {
  last_evaluation?: WorkflowRecoveryAutomaticEvaluation;
  suppression?: WorkflowRecoveryAutomaticSuppression;
  blocked_notification?: WorkflowRecoveryAutomaticBlockedNotification;
  action_id: string;
  position: number;
  status: 'pending' | 'applied' | 'suppressed';
  reason: string;
  child_job_id?: string;
  applied_at?: string;
  result?: WorkflowRecoveryReviewActionStatus;
};

