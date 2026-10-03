/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An audited issue lifecycle or ownership transition.
 */
export type IssueActivity = {
  id: string;
  action: string;
  actor_account_id?: string;
  created_at: string;
  details: Record<string, string>;
};

