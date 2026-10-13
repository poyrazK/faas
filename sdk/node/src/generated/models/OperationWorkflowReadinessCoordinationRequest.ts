/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowFulfillmentReviewCoordinationRequest } from './OperationWorkflowFulfillmentReviewCoordinationRequest.js';
import type { OperationWorkflowObligationFulfillmentRequest } from './OperationWorkflowObligationFulfillmentRequest.js';
import type { OperationWorkflowObligationReaffirmationRequest } from './OperationWorkflowObligationReaffirmationRequest.js';
import type { OperationWorkflowObligationReopeningRequest } from './OperationWorkflowObligationReopeningRequest.js';
import type { OperationWorkflowObligationTargetRequest } from './OperationWorkflowObligationTargetRequest.js';
import type { OperationWorkflowReadinessCompletionRequest } from './OperationWorkflowReadinessCompletionRequest.js';
import type { OperationWorkflowReviewResponseRequest } from './OperationWorkflowReviewResponseRequest.js';
/**
 * OperationWorkflowReadinessCoordinationRequest. Shared inbox coordination with optimistic revision checks; omission of a follow-up time clears it.
 */
export type OperationWorkflowReadinessCoordinationRequest = {
  review_coordination?: OperationWorkflowFulfillmentReviewCoordinationRequest;
  reaffirmation?: OperationWorkflowObligationReaffirmationRequest;
  reopening?: OperationWorkflowObligationReopeningRequest;
  fulfillment?: OperationWorkflowObligationFulfillmentRequest;
  target_binding?: OperationWorkflowObligationTargetRequest;
  /**
   * OperationWorkflowReadinessCoordinationRequest.review_response. Explicit human response ownership, plan and check-in linked to protected-result reviews; separate from alert acknowledgment and application execution.
   */
  review_response?: OperationWorkflowReviewResponseRequest;
  completion?: OperationWorkflowReadinessCompletionRequest;
  /**
   * OperationWorkflowReadinessCoordinationRequest.revision. Shared coordination value; does not alter application evidence.
   */
  revision: number;
  /**
   * OperationWorkflowReadinessCoordinationRequest.status. Shared coordination value; does not alter application evidence.
   */
  status: 'unread' | 'read' | 'acknowledged';
  /**
   * OperationWorkflowReadinessCoordinationRequest.owner. Shared coordination value; does not alter application evidence.
   */
  owner: string;
  /**
   * OperationWorkflowReadinessCoordinationRequest.note. Shared coordination value; does not alter application evidence.
   */
  note: string;
  /**
   * OperationWorkflowReadinessCoordinationRequest.follow_up_at. Shared coordination value; does not alter application evidence.
   */
  follow_up_at?: string;
};

