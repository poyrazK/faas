/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Desired ownership, lifecycle, or reversible regrouping action within the app. Merge adopts the destination owner/state; split opens one linked original group. Merge/split responses identify the remaining canonical issue.
 */
export type IssueActionRequest = {
  action: 'assign' | 'resolve' | 'reopen' | 'ignore' | 'merge' | 'split';
  /**
   * Required only for merge; destination in the same app and environment.
   */
  target_issue_id?: string;
  /**
   * Required only for split; linked original identity to detach.
   */
  member_issue_id?: string;
  assignee_account_id?: string;
  fixed_deployment_id?: string;
  ignored_until?: string;
  /**
   * Target team for this assignment action; clear the account assignee when selecting a team.
   */
  assignee_team_id?: string;
};

