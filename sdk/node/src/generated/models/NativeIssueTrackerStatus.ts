/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { IssueTrackerProviderStatus } from './IssueTrackerProviderStatus.js';
/**
 * Verified native connection and observed status of a linked tracker issue.
 */
export type NativeIssueTrackerStatus = {
  issue_id: string;
  link_id: string;
  installation_id: number;
  repository: string;
  repository_id: number;
  status: IssueTrackerProviderStatus;
};

