/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubject } from './OperationSubject.js';
import type { OperationWorkflowReadinessEvidence } from './OperationWorkflowReadinessEvidence.js';
/**
 * One saved transition identity and stable evidence summaries for comparison within the same ownership scope.
 */
export type OperationWorkflowReadinessSnapshotEntry = {
  /**
   * ReadinessSnapshotEntry. Customer UUID present only in account snapshots.
   */
  platform_tenant_id?: string;
  subject: OperationSubject;
  /**
   * ReadinessSnapshotEntry. Declared workflow name.
   */
  workflow: string;
  /**
   * ReadinessSnapshotEntry. Application workflow instance identifier.
   */
  instance_id: string;
  /**
   * ReadinessSnapshotEntry. Operation that reported the saved workflow state.
   */
  operation_id: string;
  /**
   * ReadinessSnapshotEntry. Application state revision observed at capture.
   */
  state_revision: number;
  /**
   * ReadinessSnapshotEntry. Pinned workflow contract version at capture.
   */
  contract_version: number;
  /**
   * ReadinessSnapshotEntry. Target Operation of the saved transition.
   */
  operation: string;
  /**
   * ReadinessSnapshotEntry. Declared source state of the saved transition.
   */
  from_state: string;
  /**
   * ReadinessSnapshotEntry. Declared destination state of the saved transition.
   */
  to_state: string;
  /**
   * ReadinessSnapshotEntry. Time of the shared per-workflow evidence evaluation or comparison completion.
   */
  evaluated_at: string;
  /**
   * ReadinessSnapshotEntry. Declared requirements were met without planned evidence at capture.
   */
  ready: boolean;
  /**
   * ReadinessSnapshotEntry. Saved unmet declared requirement codes.
   */
  reasons: Array<'state_unknown' | 'terminal' | 'transition_undeclared' | 'from_state_mismatch' | 'revision_mismatch' | 'contract_version_mismatch' | 'application_blocked' | 'dependency_unmet' | 'milestone_required' | 'policy_evidence_required' | 'dependency_required' | 'invariant_evidence_required' | 'effect_evidence_required' | 'retained_milestone_required' | 'retained_milestone_invalidated' | 'retained_milestone_expired' | 'retained_milestone_group_unmet' | 'path_selection_invalid'>;
  /**
   * ReadinessSnapshotEntry. Saved reporting and verification context; advisories never determine readiness.
   */
  advisories: Array<'state_stale' | 'deadline_overdue' | 'condition_reports_missing' | 'condition_reports_overdue' | 'condition_reporting_unknown' | 'verification_pending'>;
  /**
   * ReadinessSnapshotEntry. Sorted unique saved blocker codes for this target Operation.
   */
  blocker_codes: Array<string>;
  /**
   * ReadinessSnapshotEntry. Saved missing planned milestone names.
   */
  missing_milestones: Array<string>;
  /**
   * ReadinessSnapshotEntry. Saved prerequisite workflows without reported references.
   */
  missing_dependency_workflows: Array<string>;
  /**
   * ReadinessSnapshotEntry. Exact saved pending verification obligation count before previews.
   */
  pending_verification_count: number;
  /**
   * ReadinessSnapshotEntry. Digest of pinned contract version and declared edge requirements.
   */
  requirements_fingerprint: string;
  /**
   * ReadinessSnapshotEntry. Order-independent digest of all target blocker details.
   */
  blockers_fingerprint: string;
  /**
   * ReadinessSnapshotEntry. Digest of prerequisite statuses and evidence identities excluding moving ages and evaluation clocks.
   */
  prerequisites_fingerprint: string;
  /**
   * ReadinessSnapshotEntry. Order-independent digest of all retained pending target obligations before previews.
   */
  verifications_fingerprint: string;
  /**
   * ReadinessSnapshotEntry. Digest of reporting coverage counts and timing advisories excluding pending verification.
   */
  reporting_fingerprint: string;
  /**
   * Bounded saved evidence inventory; older snapshots may omit it.
   */
  evidence?: Array<OperationWorkflowReadinessEvidence>;
  /**
   * True only when the saved evidence inventory covers every applicable observation.
   */
  evidence_complete?: boolean;
};

