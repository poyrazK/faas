/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubject } from './OperationSubject.js';
import type { OperationWorkflowDecisionOutcomeEvidence } from './OperationWorkflowDecisionOutcomeEvidence.js';
import type { OperationWorkflowResolutionVerification } from './OperationWorkflowResolutionVerification.js';
import type { OperationWorkflowState } from './OperationWorkflowState.js';
/**
 * Current state and verification findings for one complete-history contributor. Customer responses omit tenant identifiers. Observed seconds measure the selected historical group rather than current ownership alone.
 */
export type OperationWorkflowPerformanceInstance = {
  /**
   * Exact number of overdue selected conditions for this workflow and refresh owner. Present on condition_refresh_owner contributors.
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

