/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowGapQueueGuidance } from './OperationWorkflowGapQueueGuidance.js';
import type { OperationWorkflowNewMissingConditionProgressCount } from './OperationWorkflowNewMissingConditionProgressCount.js';
/**
 * All current reporting gaps before queue filters and evidence previews. Guidance contains every eligible pinned missing expectation, including owners beyond the evidence preview; it carries no prior report facts. Blocked intervals spanning contracts are omitted.
 */
export type OperationWorkflowGapQueuePriority = {
  missing_condition_count: number;
  statuses: Array<OperationWorkflowNewMissingConditionProgressCount>;
  refresh_owners: Array<string>;
  unassigned_condition_count: number;
  guidance: Array<OperationWorkflowGapQueueGuidance>;
  blocked_seconds: number;
  explanation: string;
};

