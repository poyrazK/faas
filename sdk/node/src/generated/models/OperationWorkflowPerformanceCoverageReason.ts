/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type OperationWorkflowPerformanceCoverageReason = {
  reason: 'missing_start' | 'missing_latest' | 'revision_gap' | 'duplicate_revision' | 'out_of_order_time' | 'future_observation' | 'contract_changed' | 'state_discontinuity' | 'history_window_truncated' | 'verification_start_missing';
  workflow_count: number;
};

