/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryAutomaticQueueEntry } from './WorkflowRecoveryAutomaticQueueEntry.js';
import type { WorkflowRecoveryHealthIncident } from './WorkflowRecoveryHealthIncident.js';
import type { WorkflowRecoveryPolicyResponse } from './WorkflowRecoveryPolicyResponse.js';
/**
 * Informational automatic retry queue snapshot. Outstanding means a child has been created but final proof has not been recorded; it does not assert the child is currently running. Pending counts current policy intents only. Charges survive review pruning and policy replacement. Paused policies retain their queue and committed children continue. Missing evidence is unresolved, never success or failure.
 */
export type WorkflowRecoveryAutomaticQueue = {
  health_incident?: WorkflowRecoveryHealthIncident;
  policy: WorkflowRecoveryPolicyResponse;
  observed_at: string;
  pending: number;
  outstanding: number;
  hourly_executions: number;
  remaining_budget: number;
  cooldown_until?: string;
  entries: Array<WorkflowRecoveryAutomaticQueueEntry>;
  truncated: boolean;
};

