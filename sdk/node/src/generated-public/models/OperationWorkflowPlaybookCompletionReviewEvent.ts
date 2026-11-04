/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowPlaybookBusinessOutcome } from './OperationWorkflowPlaybookBusinessOutcome.js';
/**
 * OperationWorkflowPlaybookCompletionReviewEvent. Durable result change linked to the original closed escalation; conveys no application execution authority.
 */
export type OperationWorkflowPlaybookCompletionReviewEvent = {
  /**
   * OperationWorkflowPlaybookCompletionReviewEvent.review_id. Exact review identity.
   */
  review_id: string;
  /**
   * OperationWorkflowPlaybookCompletionReviewEvent.source_notification_id. Original closed escalation notification.
   */
  source_notification_id: string;
  /**
   * OperationWorkflowPlaybookCompletionReviewEvent.source_sequence. Linkable original inbox sequence.
   */
  source_sequence: number;
  /**
   * OperationWorkflowPlaybookCompletionReviewEvent.resolution. Original human disposition, unchanged by later evidence.
   */
  resolution: 'expected_outcome_achieved' | 'alternative_accepted' | 'follow_up_canceled';
  previous: OperationWorkflowPlaybookBusinessOutcome;
  current: OperationWorkflowPlaybookBusinessOutcome;
};

