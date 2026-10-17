/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Stored external ticket reference with provider identity and editor metadata.
 */
export type IssueTrackerLink = {
  id: string;
  /**
   * Original identity owning this reference; retained through merge and split.
   */
  original_issue_id: string;
  provider: 'github' | 'linear';
  external_key: string;
  url: string;
  created_at: string;
  updated_at: string;
  title?: string;
  created_by_account_id?: string;
  updated_by_account_id?: string;
};

