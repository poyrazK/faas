/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Selectors for the live retained follow-up queue; offset pages may move as coordination is edited.
 */
export type OperationWorkflowReadinessFollowUpOptions = {
  /**
   * OperationWorkflowReadinessFollowUpOptions.impact_priority. Exact unresolved review priority filter.
   */
  impact_priority?: 'normal' | 'high' | 'critical';
  /**
   * OperationWorkflowReadinessFollowUpOptions.impact_kind. Exact protected business result category.
   */
  impact_kind?: 'milestone' | 'outcome';
  /**
   * OperationWorkflowReadinessFollowUpOptions.impact_code. Exact protected result code, requires impact_kind.
   */
  impact_code?: string;
  /**
   * OperationWorkflowReadinessFollowUpOptions.sort. Due by default; business_impact orders overdue first, then declared unresolved review priority and due time before pagination.
   */
  sort?: 'due' | 'business_impact';
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
  /**
   * OperationWorkflowReadinessFollowUpOptions.reminder_only. Select only active published due reminders; defaults to false.
   */
  reminder_only?: boolean;
};

