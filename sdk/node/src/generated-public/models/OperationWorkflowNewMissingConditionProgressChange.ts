/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowConditionCoverageEntry } from './OperationWorkflowConditionCoverageEntry.js';
/**
 * After is a current missing expectation without evidence. Before optionally includes prior pinned declaration and evidence with result and baseline freshness. Same step and condition ID locate candidates; report_lost requires identical milestone and full declaration including expected states, budget and guidance plus prior evidence. Age expiry never removes reports. Changed declarations are expectation_changed; absent or previously inactive terminal expectations are new_expectation. Contract changes omit Before and never compare facts across contracts.
 */
export type OperationWorkflowNewMissingConditionProgressChange = {
  status: 'report_lost' | 'new_expectation' | 'still_missing' | 'expectation_changed' | 'contract_changed';
  before?: OperationWorkflowConditionCoverageEntry;
  after: OperationWorkflowConditionCoverageEntry;
  explanation: string;
};

