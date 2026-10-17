/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Replacement team display name and eligible account membership roster.
 */
export type PutIssueTeamRequest = {
  /**
   * Maximum 64 UTF-8 bytes without control characters.
   */
  name: string;
  member_account_ids: Array<string>;
};

