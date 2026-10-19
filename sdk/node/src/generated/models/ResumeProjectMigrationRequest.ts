/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectInitialSecrets } from './ProjectInitialSecrets.js';
/**
 * Explicitly resume unfinished migration work against the original reviewed source checksum.
 */
export type ResumeProjectMigrationRequest = {
  /**
   * Version of the saved receipt reviewed before this resume; stale replays are rejected.
   */
  expected_version: number;
  /**
   * Exact unfinished services approved for this resume action.
   */
  only: Array<string>;
  /**
   * Fresh migration-mode token for the original source checksum and project.
   */
  plan_token: string;
  initial_secrets?: ProjectInitialSecrets;
};

