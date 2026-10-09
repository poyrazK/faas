/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowEffectRequirement } from './OperationWorkflowEffectRequirement.js';
import type { OperationWorkflowInvariantRequirement } from './OperationWorkflowInvariantRequirement.js';
import type { OperationWorkflowPolicyRequirement } from './OperationWorkflowPolicyRequirement.js';
/**
 * One allowed state edge from the selected contract version, bound to the Operation that can report it. Required milestones must be committed with the transition in the same application transaction. This declaration does not establish that the edge is currently valid for the application's business row or authorized for the caller.
 */
export type OperationWorkflowInstanceTransition = {
  required_effects?: Array<OperationWorkflowEffectRequirement>;
  required_invariants?: Array<OperationWorkflowInvariantRequirement>;
  /**
   * For this selected contract edge, omitted means all reported dependencies; an empty array means none. Named workflows require at least one reported link and every matching link must meet its outcome requirement.
   */
  required_dependency_workflows?: Array<string>;
  required_policies?: Array<OperationWorkflowPolicyRequirement>;
  from: string;
  to: string;
  /**
   * Manifest operation name that may report this transition.
   */
  operation: string;
  /**
   * For this selected contract edge, milestone names that must be committed in the same application transaction as this transition.
   */
  required_milestones?: Array<string>;
};

