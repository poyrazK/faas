/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowEffectRequirement } from './OperationWorkflowEffectRequirement.js';
import type { OperationWorkflowInvariantRequirement } from './OperationWorkflowInvariantRequirement.js';
import type { OperationWorkflowPolicyRequirement } from './OperationWorkflowPolicyRequirement.js';
/**
 * Allowed state edge declared by a pinned application workflow definition.
 */
export type OperationWorkflowTransition = {
  required_effects?: Array<OperationWorkflowEffectRequirement>;
  required_invariants?: Array<OperationWorkflowInvariantRequirement>;
  /**
   * Omitted means all reported dependencies; an empty array means none. Named workflows require at least one reported link and every matching link must meet its outcome requirement.
   */
  required_dependency_workflows?: Array<string>;
  required_policies?: Array<OperationWorkflowPolicyRequirement>;
  from: string;
  to: string;
  /**
   * Milestone names that must be committed in the same application transaction as this transition.
   */
  required_milestones?: Array<string>;
};

