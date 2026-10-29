/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowPlaybookBusinessOutcome } from './OperationWorkflowPlaybookBusinessOutcome.js';
/**
 * OperationWorkflowPlaybookCompletionOutcome. Immutable human disposition and exact scoped advisory application evidence observed before completion; neither establishes causation nor authorizes execution.
 */
export type OperationWorkflowPlaybookCompletionOutcome = {
  /**
   * OperationWorkflowPlaybookCompletionOutcome.resolution. Human business disposition, distinct from application reports.
   */
  resolution: 'expected_outcome_achieved' | 'alternative_accepted' | 'follow_up_canceled';
  /**
   * OperationWorkflowPlaybookCompletionOutcome.alignment. Expected success matches achieved; accepted alternative matches a different terminal result permitted by an explicit pinned policy (legacy absence permits reviewer acceptance); cancellation makes no business assertion. Nonterminal evidence mismatches; incomparable evidence is unavailable.
   */
  alignment: 'matches' | 'mismatch' | 'unavailable' | 'not_applicable';
  /**
   * OperationWorkflowPlaybookCompletionOutcome.explanation. Server explanation of disposition alignment; does not prevent human closure.
   */
  explanation: string;
  observation: OperationWorkflowPlaybookBusinessOutcome;
};

