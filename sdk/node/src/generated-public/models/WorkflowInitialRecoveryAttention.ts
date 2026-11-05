/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowInitialEvidenceDiagnosis } from './WorkflowInitialEvidenceDiagnosis.js';
import type { WorkflowRecoveryAutomaticBlockedNotification } from './WorkflowRecoveryAutomaticBlockedNotification.js';
/**
 * Latest committed observation of a submitted manual initial recovery. Reads do not update it. Notification receipts describe captured historical transitions.
 */
export type WorkflowInitialRecoveryAttention = {
  reason: string;
  block_reason?: string;
  sequence: number;
  observed_at: string;
  diagnosis?: WorkflowInitialEvidenceDiagnosis;
  operator_action?: string;
  evidence_url?: string;
  cli_command?: string;
  notification?: WorkflowRecoveryAutomaticBlockedNotification;
};

