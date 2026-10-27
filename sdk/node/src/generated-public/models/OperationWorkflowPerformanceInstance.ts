/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubject } from './OperationSubject.js';
import type { OperationWorkflowConditionCoverageEntry } from './OperationWorkflowConditionCoverageEntry.js';
import type { OperationWorkflowConditionRefreshPriority } from './OperationWorkflowConditionRefreshPriority.js';
import type { OperationWorkflowDecisionOutcomeEvidence } from './OperationWorkflowDecisionOutcomeEvidence.js';
import type { OperationWorkflowGapQueuePriority } from './OperationWorkflowGapQueuePriority.js';
import type { OperationWorkflowNewMissingConditionProgressInstance } from './OperationWorkflowNewMissingConditionProgressInstance.js';
import type { OperationWorkflowResolutionVerification } from './OperationWorkflowResolutionVerification.js';
import type { OperationWorkflowState } from './OperationWorkflowState.js';
/**
 * Current state and verification findings for one complete-history contributor. Customer responses omit tenant identifiers. Observed seconds measure the selected historical group rather than current ownership alone.
 */
export type OperationWorkflowPerformanceInstance = {
  gap_queue_priority?: OperationWorkflowGapQueuePriority;
  gap_progress?: OperationWorkflowNewMissingConditionProgressInstance;
  /**
   * Exact missing condition count for the selected current state and refresh owner before preview limits.
   */
  missing_condition_count?: number;
  missing_conditions_truncated?: boolean;
  /**
   * First matching missing conditions ordered by workflow step then condition ID with latest reporting Operation pinned expectations and public guidance. No qualifying report exists in current contexts, so evidence is absent.
   */
  missing_conditions?: Array<OperationWorkflowConditionCoverageEntry>;
  refresh_priority?: OperationWorkflowConditionRefreshPriority;
  /**
   * Exact number of selected overdue conditions for this contributor. Owner dimensions restrict the count to that owner; workflow priority dimensions count all overdue conditions across owners.
   */
  refresh_evidence_count?: number;
  refresh_evidence_truncated?: boolean;
  /**
   * First 16 matching overdue condition facts in canonical identity order with pinned refresh guidance and token-bound deadlines. More conditions may qualify; refresh_evidence_count remains exact.
   */
  refresh_evidence?: Array<OperationWorkflowDecisionOutcomeEvidence>;
  decision_evidence?: OperationWorkflowDecisionOutcomeEvidence;
  platform_tenant_id?: string;
  subject: OperationSubject;
  operation_id: string;
  state: OperationWorkflowState;
  observed_seconds: number;
  resolution_verifications: Array<OperationWorkflowResolutionVerification>;
  awaiting_verification_count: number;
  resolution_verification_count: number;
};

