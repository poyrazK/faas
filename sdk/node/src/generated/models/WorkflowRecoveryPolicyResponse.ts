/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryPolicy } from './WorkflowRecoveryPolicy.js';
import type { WorkflowRecoveryPolicyAutomaticExecution } from './WorkflowRecoveryPolicyAutomaticExecution.js';
import type { WorkflowRecoveryPollingNotification } from './WorkflowRecoveryPollingNotification.js';
/**
 * Current recovery policy with trusted update actor and scheduler status. A versioned policy replacement resets its automatic failure pause and suppresses older pending automatic actions.
 */
export type WorkflowRecoveryPolicyResponse = {
  polling_notification?: WorkflowRecoveryPollingNotification;
  automatic_execution?: WorkflowRecoveryPolicyAutomaticExecution;
  app_id: string;
  version: number;
  policy: WorkflowRecoveryPolicy;
  updated_at: string;
  actor_kind: string;
  actor_id: string;
  last_evaluation_at?: string;
  status: 'disabled' | 'monitoring' | 'review_limit' | 'paused';
};

