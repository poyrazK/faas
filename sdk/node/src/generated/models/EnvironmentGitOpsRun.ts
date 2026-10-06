/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EnvironmentGitOpsPlan } from './EnvironmentGitOpsPlan.js';
/**
 * Fenced reconciliation attempt with its observed plan and resource-step journal.
 */
export type EnvironmentGitOpsRun = {
  id: string;
  source_id: string;
  revision_id: string;
  generation: number;
  status: 'running' | 'converged' | 'drifted' | 'overridden' | 'blocked' | 'partial' | 'failed' | 'superseded';
  plan: (EnvironmentGitOpsPlan | null);
  steps: null;
  error_code?: string;
  started_at: string;
  completed_at?: string;
};

