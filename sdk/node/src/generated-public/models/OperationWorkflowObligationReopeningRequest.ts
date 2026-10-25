/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowObligationTargetBinding } from './OperationWorkflowObligationTargetBinding.js';
/**
 * Explicitly reopen the latest fulfillment for an exact bound case and current source cycle, with a reason. Preserve source status, owner, note and deadline; cannot combine with other action edits. Retired sources, superseded cycles and changed/cleared bindings cannot be reopened. Reopening does not execute an application transition.
 */
export type OperationWorkflowObligationReopeningRequest = {
  binding: OperationWorkflowObligationTargetBinding;
  /**
   * Completion time of the exact latest fulfillment being reopened.
   */
  fulfilled_at: string;
  reason: string;
};

