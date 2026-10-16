/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryAutomaticQueue } from './WorkflowRecoveryAutomaticQueue.js';
import type { WorkflowRecoveryOverview } from './WorkflowRecoveryOverview.js';
/**
 * Advisory recovery automation health with bounded queue evidence. Status precedence is paused, stalled, observed blockers, current budget or cooldown, incomplete sample, then inactive or healthy. Sample progress is only from retained queue entries and is not a durable app-wide last-success timestamp. Healthy means no blocker observed in this snapshot, not scheduler or platform liveness.
 */
export type WorkflowRecoveryPolicyHealth = {
  overview?: WorkflowRecoveryOverview;
  status: 'healthy' | 'inactive' | 'paused' | 'blocked' | 'stalled' | 'unknown';
  reason: string;
  stall_after_seconds: number;
  sample_last_progress_at?: string;
  queue: WorkflowRecoveryAutomaticQueue;
};

