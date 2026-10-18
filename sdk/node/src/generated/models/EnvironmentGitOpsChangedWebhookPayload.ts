/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EnvironmentGitOpsChange } from './EnvironmentGitOpsChange.js';
/**
 * Value-free account webhook emitted atomically with a current GitOps report or completed source poll. Unchanged status is silent; first failures and later recoveries are delivered to explicit environment.gitops.changed subscribers. The drift and source axes remain independent.
 */
export type EnvironmentGitOpsChangedWebhookPayload = {
  version: 1;
  transition_id: string;
  source_id: string;
  project: string;
  environment: string;
  axis: 'drift' | 'source';
  previous_status: 'unknown' | 'in_sync' | 'drifted' | 'blocked' | 'unavailable' | 'verified';
  status: 'in_sync' | 'drifted' | 'blocked' | 'unavailable' | 'verified';
  generation: number;
  observed_version: number;
  approved_revision_id?: string;
  commit_sha?: string;
  run_id?: string;
  error_code?: string;
  observed_at: string;
  dashboard_path: string;
  changes: Array<EnvironmentGitOpsChange>;
  blocking_reasons: Array<string>;
  details_truncated: boolean;
};

