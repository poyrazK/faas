/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Application-scoped team identity, display name, complete member roster and editor metadata.
 */
export type IssueTeam = {
  id: string;
  key: string;
  name: string;
  member_account_ids: Array<string>;
  created_at: string;
  updated_at: string;
  updated_by_account_id?: string;
};

