import type {OperationWorkflowOutcomeGroup} from './OperationWorkflowOutcomeGroup.js';
export interface OperationWorkflowOutcomeSummary { group_by: 'outcome' | 'workflow' | 'customer'; evaluated_at: string; workflow_count: number; groups: OperationWorkflowOutcomeGroup[]; next_cursor?: string }
