/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryAutomaticPollingFailure } from './WorkflowRecoveryAutomaticPollingFailure.js';
/**
 * Durable consecutive failure counter and automatic execution pause for the current policy version. Updating the policy explicitly clears this pause.
 */
export type WorkflowRecoveryPolicyAutomaticExecution = {
  polling_failure?: WorkflowRecoveryAutomaticPollingFailure;
  consecutive_failures: number;
  paused_at?: string;
  pause_reason: string;
};

