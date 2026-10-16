/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Exact count of current missing conditions by comparison category before previews. Still_missing is an existing gap, while report_lost requires the identical baseline expectation and prior evidence. New or changed expectations and contract changes are distinct.
 */
export type OperationWorkflowNewMissingConditionProgressCount = {
  status: 'report_lost' | 'new_expectation' | 'still_missing' | 'expectation_changed' | 'contract_changed';
  condition_count: number;
};

