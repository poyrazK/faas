/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { NativeIssueTrackerStatus } from './NativeIssueTrackerStatus.js';
/**
 * Explicit automatic push delegation against a known policy revision and aligned native/source lifecycle snapshot.
 */
export type UpdateIssueTrackerAutoSyncRequest = {
  enabled: boolean;
  expected_revision: number;
  expected_status?: NativeIssueTrackerStatus;
  expected_issue_state?: 'open' | 'resolved';
};

