/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EnvironmentGitOpsChange } from './EnvironmentGitOpsChange.js';
import type { EnvironmentGitOpsDiagnostic } from './EnvironmentGitOpsDiagnostic.js';
/**
 * Value-free account webhook for independent drift, source and rollout axes. Rollout serving and failure outcomes are assessed from current exact-commit graph evidence when a current report completes; approval and control transactions supersede pending rollouts. Each terminal outcome is delivered once per approval generation. Pending observations stay silent. Existing drift/source transition rules and explicit environment.gitops.changed subscriptions apply. Delivery is signed and at least once; deduplicate retries by transition_id.
 */
export type EnvironmentGitOpsChangedWebhookPayload = {
  version: 1;
  transition_id: string;
  source_id: string;
  project: string;
  environment: string;
  axis: 'drift' | 'source' | 'rollout';
  previous_status: 'unknown' | 'in_sync' | 'drifted' | 'blocked' | 'unavailable' | 'verified' | 'progressing' | 'serving' | 'failed';
  status: 'in_sync' | 'drifted' | 'blocked' | 'unavailable' | 'verified' | 'serving' | 'failed' | 'superseded';
  generation: number;
  observed_version: number;
  approved_revision_id?: string;
  commit_sha?: string;
  definition_digest?: string;
  graph_id?: string;
  plan_hash?: string;
  /**
   * Safe mapped blocker codes and guidance, optionally scoped to a workload. Never includes upstream error text, logs, values or secret-reference targets.
   */
  diagnostics?: Array<EnvironmentGitOpsDiagnostic>;
  run_id?: string;
  error_code?: string;
  observed_at: string;
  dashboard_path: string;
  changes: Array<EnvironmentGitOpsChange>;
  blocking_reasons: Array<string>;
  details_truncated: boolean;
};

