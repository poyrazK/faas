/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { NativeIssueTrackerStatus } from './NativeIssueTrackerStatus.js';
import type { NativeLinearIssueTrackerStatus } from './NativeLinearIssueTrackerStatus.js';
/**
 * Explicit automatic push delegation against a known policy revision and aligned native/source lifecycle snapshot.
 */
export type UpdateIssueTrackerAutoSyncRequest = {
  enabled: boolean;
  expected_revision: number;
  expected_status?: NativeIssueTrackerStatus;
  expected_issue_state?: 'open' | 'resolved';
  expected_linear_status?: NativeLinearIssueTrackerStatus;
  /**
   * Active same-team open workflow required to enable Linear automatic pushes.
   */
  linear_open_state_id?: string;
  /**
   * Active same-team completed workflow required to enable Linear automatic pushes.
   */
  linear_resolved_state_id?: string;
};

