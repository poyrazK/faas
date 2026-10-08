/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubject } from './OperationSubject.js';
import type { OperationWorkflowState } from './OperationWorkflowState.js';
/**
 * Current retained source pointing at the selected prerequisite. Status describes that prerequisite against this source's required outcome. Needs attention is true only for an active source with an unmet prerequisite.
 */
export type OperationWorkflowDependentInstance = {
  subject: OperationSubject;
  state: OperationWorkflowState;
  required_outcome_code?: string;
  dependency_status: 'unknown' | 'waiting' | 'terminal' | 'satisfied' | 'outcome_mismatch';
  needs_attention: boolean;
};

