/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryAutomaticAction } from './WorkflowRecoveryAutomaticAction.js';
/**
 * One pending or unresolved applied automatic retry. Queued_at is the frozen review preparation time. Ages are whole nonnegative seconds relative to the queue observed_at. Waiting age is omitted until the worker records a wait; the saved reason may be historical.
 */
export type WorkflowRecoveryAutomaticQueueEntry = {
  review_id: string;
  policy_version: number;
  queued_at: string;
  queued_age_seconds: number;
  waiting_age_seconds?: number;
  action: WorkflowRecoveryAutomaticAction;
};

