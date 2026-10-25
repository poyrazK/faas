/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowConditionCoverageEntry } from './OperationWorkflowConditionCoverageEntry.js';
/**
 * Pinned public expectation and comparison category for one current reporting gap, without prior evidence facts.
 */
export type OperationWorkflowGapQueueGuidance = {
  status: 'report_lost' | 'new_expectation' | 'still_missing' | 'expectation_changed' | 'contract_changed';
  expectation: OperationWorkflowConditionCoverageEntry;
};

