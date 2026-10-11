/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Report-age status using the selected fact reporting Operation's immutable condition definition and the summary evaluation time. A positive budget yields a refresh deadline from occurred_at; overdue begins at the deadline. Missing or zero budgets remain no_budget. This does not expire facts, change reported results, or authorize execution. Republishing an explicit snapshot updates reporting freshness; it does not prove renewed business verification.
 */
export type OperationWorkflowConditionFreshness = {
  /**
   * Public refresh owner copied from the selected fact milestone on its reporting Operation pinned definition. UTF-8 byte bound; no controls or surrounding whitespace. Omission is unassigned.
   */
  refresh_owner?: string;
  /**
   * Application-declared refresh instructions copied from the selected fact pinned condition definition. UTF-8 byte bound; no controls or surrounding whitespace. Displayed as text and never executed.
   */
  refresh_instructions?: string;
  status: 'within_budget' | 'overdue' | 'no_budget';
  evidence_max_age_seconds?: number;
  refresh_due_at?: string;
  overdue_seconds?: number;
};

