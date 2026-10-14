/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Observed lifecycle and identity of an external tracker issue.
 */
export type IssueTrackerProviderStatus = {
  provider: 'github' | 'linear';
  url: string;
  id: string;
  state: 'open' | 'resolved';
  team_id?: string;
  state_id?: string;
};

