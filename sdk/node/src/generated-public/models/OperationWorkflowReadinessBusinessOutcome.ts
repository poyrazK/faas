/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Advisory comparison of the expected terminal outcome with a scoped retained application report. Missing state, stale reports, contract changes, older revisions and terminal reports without outcome codes remain unknown. Explicit outcome codes do not imply causation or authorize execution. Completion snapshots are immutable; current checks are live reads.
 */
export type OperationWorkflowReadinessBusinessOutcome = {
  /**
   * OperationWorkflowReadinessBusinessOutcome.expected_outcome_code. Scoped application-reported business result relative to the caller-selected expected terminal outcome; independent of coordination completion and readiness.
   */
  expected_outcome_code: string;
  /**
   * OperationWorkflowReadinessBusinessOutcome.checked_at. Scoped application-reported business result relative to the caller-selected expected terminal outcome; independent of coordination completion and readiness.
   */
  checked_at: string;
  /**
   * OperationWorkflowReadinessBusinessOutcome.status. Scoped application-reported business result relative to the caller-selected expected terminal outcome; independent of coordination completion and readiness.
   */
  status: 'achieved' | 'pending' | 'different_outcome' | 'unknown';
  /**
   * OperationWorkflowReadinessBusinessOutcome.explanation. Scoped application-reported business result relative to the caller-selected expected terminal outcome; independent of coordination completion and readiness.
   */
  explanation: string;
  /**
   * OperationWorkflowReadinessBusinessOutcome.reporting_operation_id. Scoped application-reported business result relative to the caller-selected expected terminal outcome; independent of coordination completion and readiness.
   */
  reporting_operation_id?: string;
  /**
   * OperationWorkflowReadinessBusinessOutcome.report_id. Scoped application-reported business result relative to the caller-selected expected terminal outcome; independent of coordination completion and readiness.
   */
  report_id?: string;
  /**
   * OperationWorkflowReadinessBusinessOutcome.state_revision. Scoped application-reported business result relative to the caller-selected expected terminal outcome; independent of coordination completion and readiness.
   */
  state_revision?: number;
  /**
   * OperationWorkflowReadinessBusinessOutcome.contract_version. Scoped application-reported business result relative to the caller-selected expected terminal outcome; independent of coordination completion and readiness.
   */
  contract_version?: number;
  /**
   * OperationWorkflowReadinessBusinessOutcome.state. Scoped application-reported business result relative to the caller-selected expected terminal outcome; independent of coordination completion and readiness.
   */
  state?: string;
  /**
   * OperationWorkflowReadinessBusinessOutcome.reported_outcome_code. Scoped application-reported business result relative to the caller-selected expected terminal outcome; independent of coordination completion and readiness.
   */
  reported_outcome_code?: string;
  /**
   * OperationWorkflowReadinessBusinessOutcome.reported_outcome_description. Scoped application-reported business result relative to the caller-selected expected terminal outcome; independent of coordination completion and readiness.
   */
  reported_outcome_description?: string;
  /**
   * OperationWorkflowReadinessBusinessOutcome.occurred_at. Scoped application-reported business result relative to the caller-selected expected terminal outcome; independent of coordination completion and readiness.
   */
  occurred_at?: string;
};

