/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowInitialEvidenceRepairReceipt } from './WorkflowInitialEvidenceRepairReceipt.js';
import type { WorkflowRecoveryPolicy } from './WorkflowRecoveryPolicy.js';
import type { WorkflowRecoveryPolicyDecision } from './WorkflowRecoveryPolicyDecision.js';
import type { WorkflowRecoveryResponsePlan } from './WorkflowRecoveryResponsePlan.js';
import type { WorkflowRecoveryReviewAutomaticExecution } from './WorkflowRecoveryReviewAutomaticExecution.js';
import type { WorkflowRecoveryReviewCompletionNotification } from './WorkflowRecoveryReviewCompletionNotification.js';
import type { WorkflowRecoveryReviewDecision } from './WorkflowRecoveryReviewDecision.js';
import type { WorkflowRecoveryReviewNotification } from './WorkflowRecoveryReviewNotification.js';
/**
 * Immutable policy evaluation with its settings snapshot and optional prepared response plan. Repeated unchanged decisions are deduplicated; retained reviews are capped at 1000 per app.
 */
export type WorkflowRecoveryPolicyReview = {
  parent_evidence_repairs?: Record<string, WorkflowInitialEvidenceRepairReceipt>;
  evidence_repairs?: Record<string, WorkflowInitialEvidenceRepairReceipt>;
  automatic_execution?: WorkflowRecoveryReviewAutomaticExecution;
  id: string;
  app_id: string;
  job_id: string;
  policy_version: number;
  policy: WorkflowRecoveryPolicy;
  observed_at: string;
  decisions: Array<WorkflowRecoveryPolicyDecision>;
  plan?: WorkflowRecoveryResponsePlan;
  review_decision?: WorkflowRecoveryReviewDecision;
  notification?: WorkflowRecoveryReviewNotification;
  completion_notification?: WorkflowRecoveryReviewCompletionNotification;
};

