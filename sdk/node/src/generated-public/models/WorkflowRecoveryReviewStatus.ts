/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryReviewActionStatus } from './WorkflowRecoveryReviewActionStatus.js';
/**
 * Application status and aggregate execution outcome. Complete requires final proof for every action; retained completion snapshots take precedence over later generations and pruned execution rows.
 */
export type WorkflowRecoveryReviewStatus = {
  review_id: string;
  observed_at: string;
  status: string;
  outcome: 'no_plan' | 'unknown' | 'pending' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'completed';
  complete: boolean;
  actions: Array<WorkflowRecoveryReviewActionStatus>;
};

