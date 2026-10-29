/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReviewResponseSourceHealth } from './OperationWorkflowReviewResponseSourceHealth.js';
/**
 * Sampled advisory response-plan condition. Clear means no observed blockers or review obligations, not business completion. Sampling stops on response resolution.
 */
export type OperationWorkflowReviewResponseHealth = {
  /**
   * OperationWorkflowReviewResponseHealth.fingerprint. Stable digest of source revisions and current observed condition; polling timestamps and recovery history are excluded.
   */
  fingerprint?: string;
  /**
   * OperationWorkflowReviewResponseHealth.status. Unavailable takes precedence over blocked, pending review and clear.
   */
  status: 'clear' | 'blocked' | 'review_required' | 'unavailable';
  /**
   * OperationWorkflowReviewResponseHealth.checked_at. Latest attempted sampling time; consult individual sources for freshness.
   */
  checked_at: string;
  /**
   * OperationWorkflowReviewResponseHealth.last_complete_check_at. Latest sample whose scoped reads all completed, including observations of missing or retired progress.
   */
  last_complete_check_at?: string;
  /**
   * OperationWorkflowReviewResponseHealth.remaining_blockers. Observed blockers plus unverified disappearances retained until positive clearance.
   */
  remaining_blockers: Array<string>;
  /**
   * OperationWorkflowReviewResponseHealth.recovered_blockers. Most recent positively verified recoveries, excluding blockers subsequently observed again.
   */
  recovered_blockers: Array<string>;
  /**
   * OperationWorkflowReviewResponseHealth.recovered_at. Time of the latest positive recovery sample.
   */
  recovered_at?: string;
  /**
   * OperationWorkflowReviewResponseHealth.sources. Condition and freshness for each fixed linked review.
   */
  sources: Array<OperationWorkflowReviewResponseSourceHealth>;
};

