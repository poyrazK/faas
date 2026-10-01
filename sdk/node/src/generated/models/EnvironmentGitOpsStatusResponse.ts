/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EnvironmentGitOpsRun } from './EnvironmentGitOpsRun.js';
import type { EnvironmentGitRevisionApproval } from './EnvironmentGitRevisionApproval.js';
import type { EnvironmentGitSource } from './EnvironmentGitSource.js';
/**
 * Source authority and the twenty most recent durable reconciliation attempts.
 */
export type EnvironmentGitOpsStatusResponse = {
  approval?: EnvironmentGitRevisionApproval;
  source: EnvironmentGitSource;
  runs: Array<EnvironmentGitOpsRun>;
};

