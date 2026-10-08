import type { OperationWorkflowEffectRequirement } from './OperationWorkflowEffectRequirement.js';
import type { OperationWorkflowInvariantRequirement } from './OperationWorkflowInvariantRequirement.js';
import type { OperationWorkflowPolicyRequirement } from './OperationWorkflowPolicyRequirement.js';
/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One allowed state edge from the selected contract version, bound to the Operation that can report it. Required milestones must be committed with the transition in the same application transaction. This declaration does not establish that the edge is currently valid for the application's business row or authorized for the caller.
 */
export type OperationWorkflowInstanceTransition = {
  from: string;
  to: string;
  /**
   * Manifest operation name that may report this transition.
   */
  operation: string;
  /**
   * Milestone names that must be committed in the same application transaction as this transition.
   */
  required_dependency_workflows?: string[]; required_effects?: OperationWorkflowEffectRequirement[]; required_invariants?: OperationWorkflowInvariantRequirement[]; required_policies?: OperationWorkflowPolicyRequirement[];
  required_milestones?: Array<string>;
};

