/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EnvironmentGitOpsChange } from './EnvironmentGitOpsChange.js';
/**
 * Observation-bound plan; blocking reasons prevent mutation and overrides prevent convergence.
 */
export type EnvironmentGitOpsPlan = {
  manager: string;
  revision: string;
  commit_sha?: string;
  generation: number;
  desired_digest: string;
  observed_version: number;
  plan_hash: string;
  changes: Array<EnvironmentGitOpsChange>;
  blocking_reasons: Array<string>;
};

