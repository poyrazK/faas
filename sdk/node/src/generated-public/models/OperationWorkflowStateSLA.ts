/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Observational SLA for the current nonterminal state visit under its pinned contract. Consecutive same-state metadata reports preserve entry time. Known history establishes the current visit from revision 1 or a contiguous prior different state. Gaps or inconsistent times/versions/budgets yield unknown status and omit inferred timestamps/durations. Breached at due_at or later. Terminal states have no current SLA.
 */
export type OperationWorkflowStateSLA = {
  /**
   * Pinned warning percentage when configured.
   */
  warning_percent?: number;
  /**
   * Warning time for a known state visit with a configured threshold.
   */
  warning_at?: string;
  evaluated_at: string;
  budget_seconds: number;
  status: 'within_budget' | 'at_risk' | 'breached' | 'unknown';
  /**
   * Retained evidence establishes the current state visit entry. Earlier workflow history can still be incomplete.
   */
  history_complete: boolean;
  entered_at?: string;
  due_at?: string;
  elapsed_seconds?: number;
  /**
   * Ceiling of remaining elapsed time. Zero at or after the due time.
   */
  remaining_seconds?: number;
  /**
   * Whole seconds elapsed beyond the budget.
   */
  breached_seconds?: number;
};

