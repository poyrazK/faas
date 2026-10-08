import type { OperationSubject } from './OperationSubject.js';
import type { OperationWorkflowState } from './OperationWorkflowState.js';
export type OperationWorkflowDependentInstance = {
  subject: OperationSubject;
  state: OperationWorkflowState;
  required_outcome_code?: string;
  dependency_status: 'unknown' | 'waiting' | 'terminal' | 'satisfied' | 'outcome_mismatch';
  needs_attention: boolean;
};
