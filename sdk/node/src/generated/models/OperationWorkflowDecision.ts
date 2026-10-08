import type { OperationWorkflowBlocker } from './OperationWorkflowBlocker.js';
import type { OperationWorkflowInstanceTransition } from './OperationWorkflowInstanceTransition.js';
export type OperationWorkflowDecision = { blockers?: OperationWorkflowBlocker[]; reason: 'state_unknown' | 'terminal' | 'no_declared_transition' | 'transitions_available' | 'state_stale' | 'application_blocked' | 'deadline_overdue' | 'dependency_waiting'; explanation: string; needs_attention: boolean; state_revision?: number; next_actions: OperationWorkflowInstanceTransition[] }
