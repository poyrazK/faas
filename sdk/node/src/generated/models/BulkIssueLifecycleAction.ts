/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Lifecycle or assignment action applied atomically to the selected canonical issue groups.
 */
export type BulkIssueLifecycleAction = {
  action: 'assign' | 'resolve' | 'ignore' | 'reopen';
  /**
   * Only for assign; omit both assignment targets to unassign.
   */
  assignee_account_id?: string;
  /**
   * Required only for resolve.
   */
  fixed_deployment_id?: string;
  /**
   * Required only for ignore; future and within 90 days.
   */
  ignored_until?: string;
  /**
   * Only for assign; mutually exclusive with assignee_account_id.
   */
  assignee_team_id?: string;
};

