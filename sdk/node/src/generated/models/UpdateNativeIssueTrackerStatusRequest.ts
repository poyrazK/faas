/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { IssueTrackerProviderStatus } from './IssueTrackerProviderStatus.js';
/**
 * Expected provider snapshot and current Gregale lifecycle to push through a native connection.
 */
export type UpdateNativeIssueTrackerStatusRequest = {
  desired_state: 'open' | 'resolved';
  expected: IssueTrackerProviderStatus;
};

