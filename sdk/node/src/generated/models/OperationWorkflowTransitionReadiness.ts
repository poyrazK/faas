/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowBlocker } from './OperationWorkflowBlocker.js';
import type { OperationWorkflowInstanceTransition } from './OperationWorkflowInstanceTransition.js';
import type { OperationWorkflowPolicyRequirement } from './OperationWorkflowPolicyRequirement.js';
import type { OperationWorkflowRelatedInstance } from './OperationWorkflowRelatedInstance.js';
import type { OperationWorkflowUnmetEffect } from './OperationWorkflowUnmetEffect.js';
import type { OperationWorkflowUnmetInvariant } from './OperationWorkflowUnmetInvariant.js';
/**
 * Ready means declared requirements match retained reports and the proposed milestone plan. It does not authorize or commit a transition or validate milestone payloads. All reported workflow prerequisites apply; only blockers targeting this Operation apply. Staleness and overdue deadlines are advisories rather than undeclared guards.
 */
export type OperationWorkflowTransitionReadiness = {
  unmet_effects?: Array<OperationWorkflowUnmetEffect>;
  unmet_invariants?: Array<OperationWorkflowUnmetInvariant>;
  /**
   * Subset of matching blockers using the invariant- namespace. Failed or unknown application checks deny readiness through application_blocked.
   */
  invariant_blockers?: Array<OperationWorkflowBlocker>;
  /**
   * Required workflow selectors without a reported prerequisite link.
   */
  missing_dependency_workflows?: Array<string>;
  missing_policies?: Array<OperationWorkflowPolicyRequirement>;
  transition: OperationWorkflowInstanceTransition;
  declared: boolean;
  ready: boolean;
  reasons: Array<'state_unknown' | 'terminal' | 'transition_undeclared' | 'from_state_mismatch' | 'revision_mismatch' | 'contract_version_mismatch' | 'application_blocked' | 'dependency_unmet' | 'milestone_required' | 'policy_evidence_required' | 'dependency_required' | 'invariant_evidence_required' | 'effect_evidence_required'>;
  advisories: Array<'state_stale' | 'deadline_overdue'>;
  state_revision?: number;
  contract_version: number;
  blockers: Array<OperationWorkflowBlocker>;
  /**
   * Unmet reference/status pairs; inspect full related states through the workflow detail.
   */
  unmet_dependencies: Array<OperationWorkflowRelatedInstance>;
  missing_milestones: Array<string>;
};

