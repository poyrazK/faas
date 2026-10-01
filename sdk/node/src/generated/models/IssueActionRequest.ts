/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Desired ownership or lifecycle action, validated within the app scope.
 */
export type IssueActionRequest = {
  action: 'assign' | 'resolve' | 'reopen' | 'ignore';
  assignee_account_id?: string;
  fixed_deployment_id?: string;
  ignored_until?: string;
};

