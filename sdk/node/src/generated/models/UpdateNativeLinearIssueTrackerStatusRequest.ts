/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { IssueTrackerProviderStatus } from './IssueTrackerProviderStatus.js';
/**
 * Explicit push against an observed provider snapshot and connection/group scope.
 */
export type UpdateNativeLinearIssueTrackerStatusRequest = {
  /**
   * Lifecycle matching the current Gregale issue state.
   */
  desired_state: 'open' | 'resolved';
  /**
   * Exact Linear status from the native preview.
   */
  expected: IssueTrackerProviderStatus;
  /**
   * Connection revision from the native preview.
   */
  expected_connection_revision: number;
  /**
   * Group fingerprint from the native preview.
   */
  expected_group_fingerprint: string;
  /**
   * Explicit active same-team workflow state UUID required when the lifecycle changes.
   */
  linear_state_id?: string;
};

