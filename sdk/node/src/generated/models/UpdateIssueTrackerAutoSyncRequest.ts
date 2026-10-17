/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { NativeIssueTrackerStatus } from './NativeIssueTrackerStatus.js';
import type { NativeLinearIssueTrackerStatus } from './NativeLinearIssueTrackerStatus.js';
/**
 * Explicit direction delegation against a known policy revision and aligned native/source lifecycle snapshot. Inbound closures are held for fixing-deployment review.
 */
export type UpdateIssueTrackerAutoSyncRequest = {
  /**
   * Defaults to outbound when omitted. Inbound and both require a fresh source intent baseline.
   */
  direction?: 'outbound' | 'inbound' | 'both';
  enabled: boolean;
  expected_revision: number;
  expected_status?: NativeIssueTrackerStatus;
  expected_issue_state?: 'open' | 'resolved';
  expected_linear_status?: NativeLinearIssueTrackerStatus;
  /**
   * Active same-team open workflow required to enable Linear automatic sync.
   */
  linear_open_state_id?: string;
  /**
   * Active same-team completed workflow required to enable Linear automatic sync.
   */
  linear_resolved_state_id?: string;
};

