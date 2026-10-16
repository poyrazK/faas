/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowNextActionCase } from './OperationWorkflowNextActionCase.js';
import type { OperationWorkflowObligationTransitionTarget } from './OperationWorkflowObligationTransitionTarget.js';
/**
 * Bind or clear one active free-form plan obligation for an exact case. Clear requires no transition; bind requires a fresh declared current-state transition. Requires the current coordination revision and unchanged status, owner, note and deadline; cannot combine with completion or review-response edits. Customer cases omit tenant IDs; operators specify the exact customer. Source obligation identity is rechecked under its write lock; evidence observation and persistence are independent.
 */
export type OperationWorkflowObligationTargetRequest = {
  obligation_id: string;
  case: OperationWorkflowNextActionCase;
  transition?: OperationWorkflowObligationTransitionTarget;
  clear?: boolean;
  reason: string;
};

