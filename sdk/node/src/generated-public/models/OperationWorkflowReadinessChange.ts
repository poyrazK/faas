/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReadinessEvidenceChange } from './OperationWorkflowReadinessEvidenceChange.js';
import type { OperationWorkflowReadinessQueueEntry } from './OperationWorkflowReadinessQueueEntry.js';
import type { OperationWorkflowReadinessSnapshotEntry } from './OperationWorkflowReadinessSnapshotEntry.js';
/**
 * Readiness comparison of the saved edge with fresh retained evidence. Contract and state boundaries are explicit.
 */
export type OperationWorkflowReadinessChange = {
  /**
   * ReadinessChange. Latest retained reporting Operation when current state is available.
   */
  current_operation_id?: string;
  /**
   * ReadinessChange. Current retained workflow state even when the saved edge is no longer applicable.
   */
  current_state?: string;
  baseline: OperationWorkflowReadinessSnapshotEntry;
  current?: OperationWorkflowReadinessQueueEntry;
  /**
   * ReadinessChange. Classification; only comparable unchanged requirements can become newly ready or newly blocked.
   */
  status: 'newly_ready' | 'newly_blocked' | 'changed' | 'unchanged' | 'state_unknown' | 'terminal' | 'transition_unavailable' | 'contract_changed' | 'baseline_ahead';
  /**
   * ReadinessChange. Declared requirement codes now unmet that were absent from the saved reasons.
   */
  added_reasons: Array<'state_unknown' | 'terminal' | 'transition_undeclared' | 'from_state_mismatch' | 'revision_mismatch' | 'contract_version_mismatch' | 'application_blocked' | 'dependency_unmet' | 'milestone_required' | 'policy_evidence_required' | 'dependency_required' | 'invariant_evidence_required' | 'effect_evidence_required' | 'retained_milestone_required' | 'retained_milestone_invalidated' | 'retained_milestone_expired' | 'retained_milestone_group_unmet' | 'path_selection_invalid'>;
  /**
   * ReadinessChange. Saved requirement codes that are no longer unmet; this does not prove application authorization.
   */
  resolved_reasons: Array<'state_unknown' | 'terminal' | 'transition_undeclared' | 'from_state_mismatch' | 'revision_mismatch' | 'contract_version_mismatch' | 'application_blocked' | 'dependency_unmet' | 'milestone_required' | 'policy_evidence_required' | 'dependency_required' | 'invariant_evidence_required' | 'effect_evidence_required' | 'retained_milestone_required' | 'retained_milestone_invalidated' | 'retained_milestone_expired' | 'retained_milestone_group_unmet' | 'path_selection_invalid'>;
  /**
   * ReadinessChange. Evidence categories whose complete stable summaries changed.
   */
  changed_categories: Array<'blockers' | 'prerequisites' | 'verification' | 'reporting'>;
  /**
   * ReadinessChange. Human-readable meaning and comparison boundary.
   */
  explanation: string;
  /**
   * Individual retained evidence changes; missing verification is never inferred fulfilled.
   */
  evidence_changes: Array<OperationWorkflowReadinessEvidenceChange>;
  /**
   * True only when both evidence inventories are complete and requirements remain comparable.
   */
  evidence_comparison_complete: boolean;
};

