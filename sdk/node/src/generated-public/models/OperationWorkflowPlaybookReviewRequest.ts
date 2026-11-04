/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * OperationWorkflowPlaybookReviewRequest. Human review of frozen checklist coordination under a shared revision fence. No caller-supplied reviewer or applicability is accepted.
 */
export type OperationWorkflowPlaybookReviewRequest = {
  /**
   * OperationWorkflowPlaybookReviewRequest.revision. Expected current source revision for requests, committed new shared revision for retained decisions.
   */
  revision: number;
  /**
   * OperationWorkflowPlaybookReviewRequest.decision. Human coordination choice; retire and replace permanently close this source checklist.
   */
  decision: 'continue' | 'retire' | 'replace';
  /**
   * OperationWorkflowPlaybookReviewRequest.reason. Required nonempty UTF-8 review reason, at most 1024 bytes and without control characters.
   */
  reason: string;
  /**
   * OperationWorkflowPlaybookReviewRequest.replacement_progress_id. Only for replace: distinct already-started active applicable checklist in the exact same case.
   */
  replacement_progress_id?: string;
  /**
   * OperationWorkflowPlaybookReviewRequest.replacement_revision. Only for replace: expected target revision, rechecked with its active disposition under the writer lock.
   */
  replacement_revision?: number;
};

