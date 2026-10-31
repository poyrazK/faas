/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowConditionCoverageEntry } from './OperationWorkflowConditionCoverageEntry.js';
/**
 * Before is a baseline missing declaration without evidence. After is an optional current expectation and its selected result/freshness. Match step and condition ID to locate replacements, but receiving a report requires identical pinned declaration and milestone. Changed vocabulary, expected states, budgets or guidance are expectation_changed. Absent current expectations are not_expected; no cross-contract after comparison is emitted. Missing reports and no-longer-expected conditions are not failed or passed business verification.
 */
export type OperationWorkflowMissingConditionProgressChange = {
  status: 'received_report' | 'still_missing' | 'expectation_changed' | 'not_expected' | 'contract_changed';
  before: OperationWorkflowConditionCoverageEntry;
  after?: OperationWorkflowConditionCoverageEntry;
  explanation: string;
};

