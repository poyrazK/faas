/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowEffectRequirement } from './OperationWorkflowEffectRequirement.js';
import type { OperationWorkflowInvariantRequirement } from './OperationWorkflowInvariantRequirement.js';
import type { OperationWorkflowPolicyRequirement } from './OperationWorkflowPolicyRequirement.js';
import type { OperationWorkflowRetainedMilestoneFreshnessRule } from './OperationWorkflowRetainedMilestoneFreshnessRule.js';
import type { OperationWorkflowRetainedMilestoneGroup } from './OperationWorkflowRetainedMilestoneGroup.js';
import type { OperationWorkflowRetainedMilestoneInvalidationRule } from './OperationWorkflowRetainedMilestoneInvalidationRule.js';
import type { OperationWorkflowRetainedMilestoneRequirement } from './OperationWorkflowRetainedMilestoneRequirement.js';
/**
 * Allowed state edge declared by a pinned application workflow definition.
 */
export type OperationWorkflowTransition = {
  retained_milestone_groups?: Array<OperationWorkflowRetainedMilestoneGroup>;
  /**
   * Maximum evidence ages pinned to this workflow edge.
   */
  retained_milestone_freshness?: Array<OperationWorkflowRetainedMilestoneFreshnessRule>;
  /**
   * Pinned edge rules linking retained prerequisites to invalidating facts from declared workflow steps. Business occurrence time determines order; an invalidating fact wins equal timestamps.
   */
  retained_milestone_invalidations?: Array<OperationWorkflowRetainedMilestoneInvalidationRule>;
  /**
   * Previously reported transaction-backed workflow facts required within the same ownership, subject, instance, and contract version; proposed evidence does not satisfy these prerequisites.
   */
  required_retained_milestones?: Array<OperationWorkflowRetainedMilestoneRequirement>;
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

