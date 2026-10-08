import type { OperationWorkflowDependency } from './OperationWorkflowDependency.js';
import type { OperationWorkflowState } from './OperationWorkflowState.js';
export interface OperationWorkflowDependencyFinding {
  kind: 'reported_blockers' | 'state_unknown' | 'outcome_unknown' | 'outcome_mismatch' | 'awaiting_application' | 'state_stale' | 'deadline_overdue' | 'cycle' | 'trace_limit';
  path: OperationWorkflowDependency[];
  explanation: string;
  state?: OperationWorkflowState;
  limit?: 'depth' | 'workflows' | 'findings' | 'dependencies';
}
