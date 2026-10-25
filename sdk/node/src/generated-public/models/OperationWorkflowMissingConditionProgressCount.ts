/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Exact baseline missing condition count for one progress status before preview limits. Received reports may report fail or unknown or already be overdue; status does not mean verification passed.
 */
export type OperationWorkflowMissingConditionProgressCount = {
  status: 'received_report' | 'still_missing' | 'expectation_changed' | 'not_expected' | 'contract_changed';
  condition_count: number;
};

