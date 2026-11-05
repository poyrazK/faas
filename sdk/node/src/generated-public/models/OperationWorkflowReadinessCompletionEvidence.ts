/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Server-observed advisory evidence before the revision-checked completion write. Workflow reports may change after this observation. Explicit boundaries never imply readiness.
 */
export type OperationWorkflowReadinessCompletionEvidence = {
  /**
   * OperationWorkflowReadinessCompletionEvidence.checked_at. Retained follow-up completion value; never changes application evidence.
   */
  checked_at: string;
  /**
   * OperationWorkflowReadinessCompletionEvidence.status. Retained follow-up completion value; never changes application evidence.
   */
  status: string;
  /**
   * OperationWorkflowReadinessCompletionEvidence.explanation. Retained follow-up completion value; never changes application evidence.
   */
  explanation: string;
  /**
   * OperationWorkflowReadinessCompletionEvidence.reporting_operation_id. Retained follow-up completion value; never changes application evidence.
   */
  reporting_operation_id?: string;
  /**
   * OperationWorkflowReadinessCompletionEvidence.state_revision. Retained follow-up completion value; never changes application evidence.
   */
  state_revision?: number;
  /**
   * OperationWorkflowReadinessCompletionEvidence.evidence_complete. Retained follow-up completion value; never changes application evidence.
   */
  evidence_complete: boolean;
  /**
   * OperationWorkflowReadinessCompletionEvidence.remaining_reasons. Retained follow-up completion value; never changes application evidence.
   */
  remaining_reasons: Array<string>;
  /**
   * OperationWorkflowReadinessCompletionEvidence.pending_verification_count. Retained follow-up completion value; never changes application evidence.
   */
  pending_verification_count: number;
  /**
   * OperationWorkflowReadinessCompletionEvidence.warnings. Retained follow-up completion value; never changes application evidence.
   */
  warnings: Array<string>;
};

