/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowNextActionCase } from './OperationWorkflowNextActionCase.js';
import type { OperationWorkflowObligationTransitionTarget } from './OperationWorkflowObligationTransitionTarget.js';
/**
 * One obligation-cycle and exact-case target binding, recorded with an authenticated actor and reason. Unrelated edits preserve it; a new cycle never inherits it. Current readiness is independently reassessed.
 */
export type OperationWorkflowObligationTargetBinding = {
  obligation_id: string;
  case: OperationWorkflowNextActionCase;
  transition: OperationWorkflowObligationTransitionTarget;
  reason: string;
  bound_at: string;
  bound_by: string;
};

