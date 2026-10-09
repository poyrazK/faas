/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowBlocker } from './OperationWorkflowBlocker.js';
import type { OperationWorkflowInstanceTransition } from './OperationWorkflowInstanceTransition.js';
/**
 * Observational explanation from the selected contract and latest reported state, independent of fact pagination. These options are not execution authorization. Required milestones must be committed with the next transition; retained historical facts do not satisfy them.
 */
export type OperationWorkflowDecision = {
  blockers?: Array<OperationWorkflowBlocker>;
  reason: 'state_unknown' | 'terminal' | 'no_declared_transition' | 'transitions_available' | 'state_stale' | 'application_blocked' | 'deadline_overdue' | 'dependency_waiting';
  explanation: string;
  /**
   * True when state is unknown, stale, overdue, has application-reported blockers, or has unresolved direct dependencies.
   */
  needs_attention: boolean;
  state_revision?: number;
  next_actions: Array<OperationWorkflowInstanceTransition>;
};

