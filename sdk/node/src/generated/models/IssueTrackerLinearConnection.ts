/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Non-secret owner-scoped Linear connection metadata; disconnected records omit provider identity.
 */
export type IssueTrackerLinearConnection = {
  /**
   * Whether a verified personal API key is stored.
   */
  connected: boolean;
  /**
   * Configuration revision used to fence concurrent changes.
   */
  revision: number;
  /**
   * Verified Linear organization identity.
   */
  workspace_id?: string;
  /**
   * Verified active Linear team identity.
   */
  team_id?: string;
  /**
   * Verified Linear user identity owning the API key.
   */
  viewer_id?: string;
};

