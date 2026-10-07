/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EnvironmentGitSourceSpec } from './EnvironmentGitSourceSpec.js';
/**
 * Durable environment authority with separate approved and fully applied revision pointers.
 */
export type EnvironmentGitSource = {
  id: string;
  account_id: string;
  project_id: string;
  environment_id: string;
  environment: string;
  source: EnvironmentGitSourceSpec;
  /**
   * Retired binding; excluded from current source lookup and controllers.
   */
  detached?: boolean;
  suspended: boolean;
  generation: number;
  intent_version: number;
  approved_revision_id?: string;
  applied_revision_id?: string;
  source_checked_at?: string;
  source_error_code?: string;
  /**
   * Last verified candidate at the bound ref; discovery does not grant approval.
   */
  source_commit_sha?: string;
  source_definition_digest?: string;
  /**
   * Last successful candidate verification, preserved when a later poll fails.
   */
  source_verified_at?: string;
  created_at: string;
  updated_at: string;
};

