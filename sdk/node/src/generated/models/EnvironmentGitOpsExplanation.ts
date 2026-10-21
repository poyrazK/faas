/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EnvironmentGitOpsDiagnostic } from './EnvironmentGitOpsDiagnostic.js';
import type { EnvironmentWorkloadProgress } from './EnvironmentWorkloadProgress.js';
/**
 * Read-only assessment of recorded source, graph and workload facts. Unknown or historical evidence never grants readiness or execution authority.
 */
export type EnvironmentGitOpsExplanation = {
  read_only: boolean;
  assessed_at: string;
  status: 'unknown' | 'blocked' | 'progressing' | 'reporting' | 'serving' | 'degraded';
  summary: string;
  source_id: string;
  generation: number;
  intent_version: number;
  source_freshness: 'unknown' | 'stale' | 'fresh';
  observed_commit_sha?: string;
  approved_revision_id?: string;
  applied_revision_id?: string;
  serving_revision_id?: string;
  issues: Array<EnvironmentGitOpsDiagnostic>;
  workloads: Array<EnvironmentWorkloadProgress>;
};

