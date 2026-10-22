/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An audited issue lifecycle, ownership, or manual regrouping transition.
 */
export type IssueActivity = {
  id: string;
  action: string;
  readonly original_issue_id?: string;
  actor_account_id?: string;
  created_at: string;
  details: Record<string, string>;
};

