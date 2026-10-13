/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowObligationTargetBinding } from './OperationWorkflowObligationTargetBinding.js';
/**
 * Immutable explicit ownership/deadline change for one exact pending review cycle, including prior values and authenticated actor/reason. Does not modify original work or execute an application transition.
 */
export type OperationWorkflowFulfillmentReviewCoordinationChange = {
  review_id: string;
  binding: OperationWorkflowObligationTargetBinding;
  fulfilled_at: string;
  from_owner: string;
  owner: string;
  previous_due_at?: string;
  due_at?: string;
  reason: string;
  at: string;
  actor: string;
};

