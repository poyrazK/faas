/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryExecutionAttentionTarget } from './WorkflowRecoveryExecutionAttentionTarget.js';
/**
 * Generation-fenced advisory execution monitoring. Capacity and scheduled wait counters describe underlying waits and may overlap deadline exceeded counts. Only the first attention event per job is captured. Detection never cancels or retries work.
 */
export type WorkflowRecoveryExecutionAttention = {
  observed_at: string;
  deadline_seconds: number;
  stall_seconds: number;
  stalled_count: number;
  deadline_exceeded_count: number;
  capacity_waiting_count: number;
  scheduled_waiting_count: number;
  unknown_count: number;
  needs_attention: boolean;
  notification_captured: boolean;
  targets: Array<WorkflowRecoveryExecutionAttentionTarget>;
};

