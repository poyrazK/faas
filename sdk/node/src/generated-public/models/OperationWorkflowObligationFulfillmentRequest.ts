/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowObligationTargetBinding } from './OperationWorkflowObligationTargetBinding.js';
/**
 * Explicit fulfillment of one current bound case/cycle. Must preserve source status, owner, note and deadline and cannot combine with target binding, response or shared completion edits. The full binding fences replacement or rebinding. Evidence must be a retained application report of the exact target transition, published after binding; current readiness is insufficient.
 */
export type OperationWorkflowObligationFulfillmentRequest = {
  binding: OperationWorkflowObligationTargetBinding;
  reason: string;
  evidence_operation_id: string;
  evidence_report_id: string;
};

