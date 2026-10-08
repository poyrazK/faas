/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubject } from './OperationSubject.js';
import type { OperationWorkflowRelatedInstance } from './OperationWorkflowRelatedInstance.js';
import type { OperationWorkflowState } from './OperationWorkflowState.js';
export type OperationWorkflowAttentionEntry = {
  /**
   * Unresolved direct references on an active source. These entries carry dependency and status; target state is available in the workflow instance detail.
   */
  dependency_attention?: Array<OperationWorkflowRelatedInstance>;
  app_id: string;
  scope: string;
  /**
   * Present only in account-operator responses.
   */
  platform_tenant_id?: string;
  subject: OperationSubject;
  /**
   * Operation that published the current state report.
   */
  operation_id: string;
  state: OperationWorkflowState;
  reasons: Array<'blocked' | 'stale' | 'overdue' | 'dependency'>;
};

