/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowBlocker } from './OperationWorkflowBlocker.js';
import type { OperationWorkflowInstanceTransition } from './OperationWorkflowInstanceTransition.js';
import type { OperationWorkflowPolicyRequirement } from './OperationWorkflowPolicyRequirement.js';
import type { OperationWorkflowRelatedInstance } from './OperationWorkflowRelatedInstance.js';
import type { OperationWorkflowResolutionVerification } from './OperationWorkflowResolutionVerification.js';
import type { OperationWorkflowRetainedMilestone } from './OperationWorkflowRetainedMilestone.js';
import type { OperationWorkflowRetainedMilestoneGroupResult } from './OperationWorkflowRetainedMilestoneGroupResult.js';
import type { OperationWorkflowTransitionReporting } from './OperationWorkflowTransitionReporting.js';
import type { OperationWorkflowUnmetEffect } from './OperationWorkflowUnmetEffect.js';
import type { OperationWorkflowUnmetInvariant } from './OperationWorkflowUnmetInvariant.js';
/**
 * Ready means declared requirements match retained reports and the proposed milestone plan. It does not authorize or commit a transition or validate milestone payloads. All reported workflow prerequisites apply; only blockers targeting this Operation apply. Staleness, overdue deadlines, current-state reporting context and pending resolution proofs are advisories rather than undeclared guards. Reporting context uses current pinned expected_states, not invented required transition conditions. Planned reports do not replace this retained context.
 */
export type OperationWorkflowTransitionReadiness = {
  reporting?: OperationWorkflowTransitionReporting;
  /**
   * Exact retained pending resolution proof count targeting the transition Operation before previews, across retained workflow reports. Advisory rather than a new transition guard.
   */
  pending_verification_count: number;
  pending_verifications_truncated: boolean;
  /**
   * Canonical first matching pending proofs with declared owners and milestone references. Verified proofs and other target Operations are excluded.
   */
  pending_verifications?: Array<OperationWorkflowResolutionVerification>;
  retained_milestone_groups?: Array<OperationWorkflowRetainedMilestoneGroupResult>;
  /**
   * Status and latest retained evidence for each prerequisite declared on this edge. Absent facts remain awaiting verification; invalidating facts mark prerequisites invalidated. Both statuses prevent readiness.
   */
  retained_milestones?: Array<OperationWorkflowRetainedMilestone>;
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
  reasons: Array<'state_unknown' | 'terminal' | 'transition_undeclared' | 'from_state_mismatch' | 'revision_mismatch' | 'contract_version_mismatch' | 'application_blocked' | 'dependency_unmet' | 'milestone_required' | 'policy_evidence_required' | 'dependency_required' | 'invariant_evidence_required' | 'effect_evidence_required' | 'retained_milestone_required' | 'retained_milestone_invalidated' | 'retained_milestone_expired' | 'retained_milestone_group_unmet' | 'path_selection_invalid'>;
  advisories: Array<'state_stale' | 'deadline_overdue' | 'condition_reports_missing' | 'condition_reports_overdue' | 'condition_reporting_unknown' | 'verification_pending'>;
  state_revision?: number;
  contract_version: number;
  blockers: Array<OperationWorkflowBlocker>;
  /**
   * Unmet reference/status pairs; inspect full related states through the workflow detail.
   */
  unmet_dependencies: Array<OperationWorkflowRelatedInstance>;
  missing_milestones: Array<string>;
};

