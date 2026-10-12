/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A scoped source observation for one fixed linked review; unavailable observations never establish review resolution.
 */
export type OperationWorkflowReviewResponseSourceHealth = {
  /**
   * OperationWorkflowReviewResponseSourceHealth.progress_revision. Retained linked progress revision; absent if progress could not be read.
   */
  progress_revision?: number;
  /**
   * OperationWorkflowReviewResponseSourceHealth.state_revision. Retained application state revision sampled with this case; absent if unavailable.
   */
  state_revision?: number;
  /**
   * OperationWorkflowReviewResponseSourceHealth.review_index. Zero-based index into the response's fixed reviews array.
   */
  review_index: number;
  /**
   * OperationWorkflowReviewResponseSourceHealth.status. Whether the linked progress, step and declared evidence could be observed without availability or staleness issues.
   */
  status: 'observed' | 'unavailable';
  /**
   * OperationWorkflowReviewResponseSourceHealth.checked_at. Latest attempt to observe this source.
   */
  checked_at: string;
  /**
   * OperationWorkflowReviewResponseSourceHealth.last_successful_check_at. Most recent available source observation, retained through later read failures.
   */
  last_successful_check_at?: string;
  /**
   * OperationWorkflowReviewResponseSourceHealth.review_required. Current validity review obligation when the step is observable; unavailable sources cannot prove it cleared.
   */
  review_required: boolean;
  /**
   * OperationWorkflowReviewResponseSourceHealth.blockers. Blockers observed in this attempt; consult summary remaining blockers for unverified prior observations.
   */
  blockers: Array<string>;
  /**
   * OperationWorkflowReviewResponseSourceHealth.explanation. Availability explanation without backend error details.
   */
  explanation: string;
};

