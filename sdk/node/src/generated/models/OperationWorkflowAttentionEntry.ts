import type { OperationWorkflowRelatedInstance } from './OperationWorkflowRelatedInstance.js';
import type { OperationSubject } from './OperationSubject.js';
import type { OperationWorkflowState } from './OperationWorkflowState.js';
export type OperationWorkflowAttentionEntry = { dependency_attention?: OperationWorkflowRelatedInstance[]; app_id: string; scope: string; platform_tenant_id?: string; subject: OperationSubject; operation_id: string; state: OperationWorkflowState; reasons: Array<'blocked' | 'stale' | 'overdue' | 'dependency'> };
