import type { OperationWorkflowPlannedEffect } from './OperationWorkflowPlannedEffect.js';
import type { OperationWorkflowPlannedInvariant } from './OperationWorkflowPlannedInvariant.js';
import type { OperationWorkflowPlannedDecision } from './OperationWorkflowPlannedDecision.js';
import type { OperationSubject } from './OperationSubject.js';
export interface OperationWorkflowReadinessRequest {
 app_id?: string; tenant_id?: string; scope: string; subject: OperationSubject;
 workflow: string; instance_id: string; operation: string; from_state: string; to_state: string;
 milestones?: string[]; state_revision?: number; contract_version?: number;
}
