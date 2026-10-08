import type {OperationWorkflowAttentionStats} from './OperationWorkflowAttentionStats.js';
import type {OperationWorkflowAttentionGroup} from './OperationWorkflowAttentionGroup.js';
export interface OperationWorkflowAttentionSummary { group_by: 'workflow' | 'blocker_code' | 'target_operation' | 'dependency_status' | 'required_outcome_code' | 'customer'; evaluated_at: string; totals: OperationWorkflowAttentionStats; groups: OperationWorkflowAttentionGroup[]; next_cursor?: string }
