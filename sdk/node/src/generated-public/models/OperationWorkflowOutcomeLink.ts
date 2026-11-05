/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Observed report-to-outcome path on the same retained history page. Links reported sequence without establishing business causation; durations are meaningful only for linked status.
 */
export type OperationWorkflowOutcomeLink = {
  status: 'linked' | 'outcome_unavailable' | 'history_incomplete' | 'timing_inconsistent';
  summary: string;
  outcome_report_id?: string;
  outcome_operation_id?: string;
  outcome_revision?: number;
  outcome_code?: string;
  /**
   * Observed duration from this report to the linked outcome. Meaningful only when status is linked.
   */
  elapsed_seconds: number;
  /**
   * Observed intervals with at least one reported blocker. Overlapping blockers count once. Meaningful only when status is linked.
   */
  blocked_seconds: number;
};

