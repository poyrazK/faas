import type { OperationSubject } from './OperationSubject.js';
import type { OperationWorkflowTransitionReadiness } from './OperationWorkflowTransitionReadiness.js';
export interface OperationWorkflowReadinessResponse {
 subject: OperationSubject; workflow: string; instance_id: string; evaluated_at: string;
 readiness: OperationWorkflowTransitionReadiness;
}
