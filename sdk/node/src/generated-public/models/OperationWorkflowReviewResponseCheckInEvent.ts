/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReviewResponse } from './OperationWorkflowReviewResponse.js';
/**
 * OperationWorkflowReviewResponseCheckInEvent. Frozen response plan, linked reviews and routing at one missed check-in, separate from business outcome and alert acknowledgment.
 */
export type OperationWorkflowReviewResponseCheckInEvent = {
  /**
   * OperationWorkflowReviewResponseCheckInEvent.source_notification_id. Original risk alert identity.
   */
  source_notification_id: string;
  /**
   * OperationWorkflowReviewResponseCheckInEvent.source_sequence. Original alert sequence within this inbox.
   */
  source_sequence: number;
  /**
   * OperationWorkflowReviewResponseCheckInEvent.checked_at. Periodic evaluation time.
   */
  checked_at: string;
  /**
   * OperationWorkflowReviewResponseCheckInEvent.check_in_at. Original missed response check-in; handoff does not reset it.
   */
  check_in_at: string;
  /**
   * OperationWorkflowReviewResponseCheckInEvent.previous_owner. Response owner before the overdue check.
   */
  previous_owner: string;
  /**
   * OperationWorkflowReviewResponseCheckInEvent.owner. Effective responsible team after guarded fallback.
   */
  owner: string;
  /**
   * OperationWorkflowReviewResponseCheckInEvent.reassigned. Whether guarded fallback changed both response responsibility and inbox ownership.
   */
  reassigned: boolean;
  /**
   * OperationWorkflowReviewResponseCheckInEvent.response. Immutable response snapshot with its plan, links and bounded change history. This is informational context, not a new editable response.
   */
  response: OperationWorkflowReviewResponse;
};

