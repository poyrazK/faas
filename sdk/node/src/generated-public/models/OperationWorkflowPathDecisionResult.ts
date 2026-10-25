/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationBusinessDecision } from './OperationBusinessDecision.js';
import type { OperationWorkflowPolicyRequirement } from './OperationWorkflowPolicyRequirement.js';
/**
 * Comparison of a pinned path decision requirement with planned application evidence. This is an observation and grants no execution authority.
 */
export type OperationWorkflowPathDecisionResult = {
  requirement: OperationWorkflowPolicyRequirement;
  reported?: OperationBusinessDecision;
  satisfied: boolean;
};

