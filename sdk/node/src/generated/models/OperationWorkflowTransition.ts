import type { OperationWorkflowEffectRequirement } from './OperationWorkflowEffectRequirement.js';
import type { OperationWorkflowInvariantRequirement } from './OperationWorkflowInvariantRequirement.js';
import type { OperationWorkflowPolicyRequirement } from './OperationWorkflowPolicyRequirement.js';
/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Allowed state edge declared by a pinned application workflow definition.
 */
export type OperationWorkflowTransition = {
  from: string;
  to: string;
  /**
   * Milestone names that must be committed in the same application transaction as this transition.
   */
  required_dependency_workflows?: string[]; required_effects?: OperationWorkflowEffectRequirement[]; required_invariants?: OperationWorkflowInvariantRequirement[]; required_policies?: OperationWorkflowPolicyRequirement[];
  required_milestones?: Array<string>;
};

