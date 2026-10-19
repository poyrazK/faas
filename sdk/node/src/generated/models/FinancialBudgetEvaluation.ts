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
};

