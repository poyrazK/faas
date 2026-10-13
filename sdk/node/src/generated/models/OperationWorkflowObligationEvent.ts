/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowNextActionCase } from './OperationWorkflowNextActionCase.js';
import type { OperationWorkflowObligationFulfillment } from './OperationWorkflowObligationFulfillment.js';
import type { OperationWorkflowObligationReopening } from './OperationWorkflowObligationReopening.js';
import type { OperationWorkflowObligationReplacement } from './OperationWorkflowObligationReplacement.js';
import type { OperationWorkflowObligationTargetBinding } from './OperationWorkflowObligationTargetBinding.js';
/**
 * Retained source receipt anchored to the saved obligation; observed_assignment describes the delivered assignment snapshot, not a newly audited write. Available event coverage is partial.
 */
export type OperationWorkflowObligationEvent = {
  reopening?: OperationWorkflowObligationReopening;
  fulfillment?: OperationWorkflowObligationFulfillment;
  target_binding?: OperationWorkflowObligationTargetBinding;
  replacement?: OperationWorkflowObligationReplacement;
  /**
   * Affected case identities captured at the source change. Legacy receipts may omit them; case views exclude unscoped receipts.
   */
  cases?: Array<OperationWorkflowNextActionCase>;
  action_kind?: string;
  shared_plan?: boolean;
  /**
   * Exact obligation identity for mutation-time receipts; older inferred observations may omit it.
   */
  obligation_id?: string;
  kind: 'observed_assignment' | 'accepted' | 'declined' | 'reassigned' | 'completed' | 'canceled' | 'rescheduled' | 'assigned' | 'changed' | 'superseded' | 'replaces' | 'replaced_by' | 'target_bound' | 'target_cleared' | 'fulfilled' | 'reopened';
  at: string;
  actor?: string;
  explanation: string;
};

