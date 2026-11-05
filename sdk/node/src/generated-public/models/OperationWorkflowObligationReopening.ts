/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowObligationTargetBinding } from './OperationWorkflowObligationTargetBinding.js';
/**
 * Immutable human decision to restore one current-source case/cycle to work, retaining the original fulfillment and its application evidence. Owner notices are delivered by the worker, with durable duplicate suppression. A subsequent fulfillment requires a report published after this reopening.
 */
export type OperationWorkflowObligationReopening = {
  /**
   * Source notification sequence within the delivering readiness subscription.
   */
  source_sequence: number;
  binding: OperationWorkflowObligationTargetBinding;
  fulfilled_at: string;
  reason: string;
  action_kind: 'response_check_in' | 'coordinator_completion' | 'follow_up';
  owner: string;
  reopened_at: string;
  reopened_by: string;
};

