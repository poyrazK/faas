/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { IssueTrackerProviderStatus } from './IssueTrackerProviderStatus.js';
/**
 * Credential-free Linear lifecycle preview pinned to the current issue group and owner connection.
 */
export type NativeLinearIssueTrackerStatus = {
  /**
   * Current canonical Gregale issue identity.
   */
  issue_id: string;
  /**
   * Stored Linear reference identity.
   */
  link_id: string;
  /**
   * Verified connection revision used to fence later pushes.
   */
  connection_revision: number;
  /**
   * Opaque scope fingerprint tracking canonical membership and grouping rules.
   */
  group_fingerprint: string;
  /**
   * Verified Linear workspace identity.
   */
  workspace_id: string;
  /**
   * Verified connected team matching the linked ticket.
   */
  team_id: string;
  /**
   * Verified identity owning the stored API key.
   */
  viewer_id: string;
  /**
   * Supported linked Linear status including ticket and team and workflow UUIDs.
   */
  status: IssueTrackerProviderStatus;
};

