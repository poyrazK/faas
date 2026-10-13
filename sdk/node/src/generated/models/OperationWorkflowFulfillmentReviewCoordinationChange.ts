/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowObligationTargetBinding } from './OperationWorkflowObligationTargetBinding.js';
/**
 * Immutable explicit ownership/deadline change for one exact pending review cycle, including prior values and authenticated actor/reason. Does not modify original work or execute an application transition.
 */
export type OperationWorkflowFulfillmentReviewCoordinationChange = {
  from_escalation_owner?: string;
  previous_escalate_after_seconds?: number;
  /**
   * New escalation team recorded by this review coordination change; empty or absent means escalation is disabled.
   */
  escalation_owner?: string;
  /**
   * New grace period in seconds recorded by this review coordination change; zero or absent means escalation is disabled.
   */
  escalate_after_seconds?: number;
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

