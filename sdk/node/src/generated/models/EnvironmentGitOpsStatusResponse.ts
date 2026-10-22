/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EnvironmentGitOpsExplanation } from './EnvironmentGitOpsExplanation.js';
import type { EnvironmentGitOpsRun } from './EnvironmentGitOpsRun.js';
import type { EnvironmentGitRevisionApproval } from './EnvironmentGitRevisionApproval.js';
import type { EnvironmentGitSource } from './EnvironmentGitSource.js';
import type { EnvironmentWorkloadActivationEvidence } from './EnvironmentWorkloadActivationEvidence.js';
import type { EnvironmentWorkloadRetirement } from './EnvironmentWorkloadRetirement.js';
/**
 * Source authority, current workload qualification evidence, and the twenty most recent durable reconciliation attempts.
 */
export type EnvironmentGitOpsStatusResponse = {
  explanation?: EnvironmentGitOpsExplanation;
  approval?: EnvironmentGitRevisionApproval;
  workload_retirements?: Array<EnvironmentWorkloadRetirement>;
  workload_evidence?: EnvironmentWorkloadActivationEvidence;
  source: EnvironmentGitSource;
  runs: Array<EnvironmentGitOpsRun>;
};

