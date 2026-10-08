import type { OperationWorkflowUnmetEffect } from './OperationWorkflowUnmetEffect.js';
import type { OperationWorkflowUnmetInvariant } from './OperationWorkflowUnmetInvariant.js';
import type { OperationWorkflowPolicyRequirement } from './OperationWorkflowPolicyRequirement.js';
import type { OperationWorkflowInstanceTransition } from './OperationWorkflowInstanceTransition.js';
import type { OperationWorkflowBlocker } from './OperationWorkflowBlocker.js';
import type { OperationWorkflowRelatedInstance } from './OperationWorkflowRelatedInstance.js';
export interface OperationWorkflowTransitionReadiness {
 transition: OperationWorkflowInstanceTransition; declared: boolean; ready: boolean;
 reasons: Array<'state_unknown'|'terminal'|'transition_undeclared'|'from_state_mismatch'|'revision_mismatch'|'contract_version_mismatch'|'application_blocked'|'dependency_unmet'|'milestone_required'|'policy_evidence_required'|'dependency_required'|'invariant_evidence_required'|'effect_evidence_required'>;
 advisories: Array<'state_stale'|'deadline_overdue'>;
 state_revision?: number; contract_version: number; blockers: OperationWorkflowBlocker[];
 unmet_dependencies: OperationWorkflowRelatedInstance[]; missing_milestones: string[];
}
