/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EnvironmentGitOpsRun } from './EnvironmentGitOpsRun.js';
import type { EnvironmentGitRevisionApproval } from './EnvironmentGitRevisionApproval.js';
import type { EnvironmentGitSource } from './EnvironmentGitSource.js';
import type { EnvironmentWorkloadActivationEvidence } from './EnvironmentWorkloadActivationEvidence.js';
/**
 * Source authority, current workload qualification evidence, and the twenty most recent durable reconciliation attempts.
 */
export type EnvironmentGitOpsStatusResponse = {
  approval?: EnvironmentGitRevisionApproval;
  workload_evidence?: EnvironmentWorkloadActivationEvidence;
  source: EnvironmentGitSource;
  runs: Array<EnvironmentGitOpsRun>;
};

