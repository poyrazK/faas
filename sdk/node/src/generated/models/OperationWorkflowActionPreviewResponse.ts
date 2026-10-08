/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubject } from './OperationSubject.js';
import type { OperationWorkflowState } from './OperationWorkflowState.js';
import type { OperationWorkflowTransitionReadiness } from './OperationWorkflowTransitionReadiness.js';
export type OperationWorkflowActionPreviewResponse = {
  subject: OperationSubject;
  workflow: string;
  instance_id: string;
  evaluated_at: string;
  state?: OperationWorkflowState;
  state_revision?: number;
  contract_version: number;
  reason: 'state_unknown' | 'terminal' | 'no_declared_transition' | 'actions_available';
  /**
   * Declared candidates evaluated with no planned milestones or decisions. Availability does not mean readiness or authorization.
   */
  actions: Array<OperationWorkflowTransitionReadiness>;
  action_count: number;
  /**
   * The 100-action cap was reached. Filter by operation to narrow the preview; this endpoint has no cursor.
   */
  has_more: boolean;
};

