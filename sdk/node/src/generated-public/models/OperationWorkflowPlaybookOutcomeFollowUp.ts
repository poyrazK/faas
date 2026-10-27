/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowPlaybookBusinessOutcome } from './OperationWorkflowPlaybookBusinessOutcome.js';
import type { OperationWorkflowPlaybookProgressItem } from './OperationWorkflowPlaybookProgressItem.js';
/**
 * OperationWorkflowPlaybookOutcomeFollowUp. Human follow-up for one pinned reported outcome. First review captures an advisory pre-write business observation; later reviews do not rewrite it or assert execution success.
 */
export type OperationWorkflowPlaybookOutcomeFollowUp = {
  /**
   * OperationWorkflowPlaybookOutcomeFollowUp.outcome_code. Exact pinned outcome associated with this list.
   */
  outcome_code: string;
  observation: OperationWorkflowPlaybookBusinessOutcome;
  /**
   * OperationWorkflowPlaybookOutcomeFollowUp.items. Pinned ordered steps with scoped evidence references and human review stamps.
   */
  items: Array<OperationWorkflowPlaybookProgressItem>;
  /**
   * OperationWorkflowPlaybookOutcomeFollowUp.completed_count. Reviewed completed steps, independent of original checklist completion.
   */
  completed_count: number;
  /**
   * OperationWorkflowPlaybookOutcomeFollowUp.checklist_complete. All declared steps reviewed complete; never proves application state or execution.
   */
  checklist_complete: boolean;
};

