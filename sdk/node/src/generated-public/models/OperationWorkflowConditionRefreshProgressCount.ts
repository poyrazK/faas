/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Exact condition count for one reporting progress status, computed before preview truncation. Refreshed means a newer report within its pinned age budget; still_overdue includes newer reports already overdue. Unreported denotes an absent exact condition and decision/rule identity, not proof of a cleared business condition. Contract changes are not compared across definitions. Budget removal is not a refresh.
 */
export type OperationWorkflowConditionRefreshProgressCount = {
  status: 'refreshed' | 'still_overdue' | 'unreported' | 'contract_changed' | 'budget_removed';
  condition_count: number;
};

