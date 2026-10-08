import type {OperationSubject} from './OperationSubject.js';
import type {OperationWorkflowState} from './OperationWorkflowState.js';
export interface OperationWorkflowOutcomeEntry { app_id: string; scope: string; platform_tenant_id?: string; subject: OperationSubject; operation_id: string; state: OperationWorkflowState }
