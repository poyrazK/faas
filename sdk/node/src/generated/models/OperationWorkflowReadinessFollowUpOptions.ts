/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Selectors for the live retained follow-up queue; offset pages may move as coordination is edited.
 */
export type OperationWorkflowReadinessFollowUpOptions = {
  /**
   * OperationWorkflowReadinessFollowUpOptions.app_id. Follow-up queue selector; customer tenant derives from authentication.
   */
  app_id?: string;
  /**
   * OperationWorkflowReadinessFollowUpOptions.tenant_id. Follow-up queue selector; customer tenant derives from authentication.
   */
  tenant_id?: string;
  /**
   * OperationWorkflowReadinessFollowUpOptions.scope. Follow-up queue selector; customer tenant derives from authentication.
   */
  scope: string;
  /**
   * OperationWorkflowReadinessFollowUpOptions.owner. Follow-up queue selector; customer tenant derives from authentication.
   */
  owner?: string;
  /**
   * OperationWorkflowReadinessFollowUpOptions.workflow. Follow-up queue selector; customer tenant derives from authentication.
   */
  workflow?: string;
  /**
   * OperationWorkflowReadinessFollowUpOptions.due. Follow-up queue selector; customer tenant derives from authentication.
   */
  due?: 'all' | 'overdue' | 'upcoming';
  /**
   * OperationWorkflowReadinessFollowUpOptions.offset. Follow-up queue selector; customer tenant derives from authentication.
   */
  offset?: number;
  /**
   * OperationWorkflowReadinessFollowUpOptions.limit. Follow-up queue selector; customer tenant derives from authentication.
   */
  limit?: number;
};

