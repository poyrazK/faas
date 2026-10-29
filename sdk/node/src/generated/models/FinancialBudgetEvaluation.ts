/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Retained evidence boundary; an absent known_millicents means evaluation unavailable, never zero. Partial known costs can trigger labelled notifications; stale observations cannot create new alerts.
 */
export type FinancialBudgetEvaluation = {
  period_start: string;
  period_end: string;
  as_of: string;
  evaluated_at: string;
  evidence_through_id: number;
  known_millicents?: number;
  coverage_complete: boolean;
  fresh: boolean;
  reasons: Array<string>;
  /**
   * Month-end amount at the current run rate: an estimate, not an invoice. Absent, with projection_reason, when a selected meter has no forecast or the evidence is incomplete or stale.
   */
  projected_millicents?: number;
  /**
   * Why no projection is available, for example compute:insufficient_history (less than a day of evidence) or incomplete_evidence.
   */
  projection_reason?: string;
};

