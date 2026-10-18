/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EnvironmentGitOpsPlan } from './EnvironmentGitOpsPlan.js';
/**
 * Read-only candidate plan pinned to a source generation and observed intent version. Values are omitted. The plan cannot authorize approval, adoption, deployment or runtime readiness. Re-run after source or environment changes.
 */
export type PreviewEnvironmentGitPlanResponse = {
  source_id: string;
  provider: 'github' | 'gitlab';
  project: string;
  environment: string;
  observed_at: string;
  read_only: boolean;
  values_redacted: boolean;
  plan: EnvironmentGitOpsPlan;
};

