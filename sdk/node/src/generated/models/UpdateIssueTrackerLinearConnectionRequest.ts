/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explicit connection or disconnection against a known revision.
 */
export type UpdateIssueTrackerLinearConnectionRequest = {
  /**
   * True connects a verified personal API key; false erases it.
   */
  connected: boolean;
  /**
   * Revision from the latest connection metadata read.
   */
  expected_revision: number;
  /**
   * Active team UUID required only when connecting.
   */
  team_id?: string;
  /**
   * Personal API key required only when connecting; never returned or logged.
   */
  api_key?: string;
  /**
   * Optional Linear webhook signing secret stored with the connection. Omission on replacement disables callbacks. Never returned or logged.
   */
  webhook_secret?: string;
};

