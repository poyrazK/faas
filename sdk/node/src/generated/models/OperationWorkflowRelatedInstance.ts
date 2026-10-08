import type {OperationWorkflowDependency} from './OperationWorkflowDependency.js';
import type {OperationWorkflowState} from './OperationWorkflowState.js';
export interface OperationWorkflowRelatedInstance { dependency: OperationWorkflowDependency; status: 'unknown' | 'waiting' | 'terminal' | 'satisfied' | 'outcome_mismatch'; state?: OperationWorkflowState }
