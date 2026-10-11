/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DebugDependencyLatencyItem } from './DebugDependencyLatencyItem.js';
/**
 * Dependency latency split by deployment (ADR-958): baseline_* fields describe the previous deployment, current_* the compared one. Regressions first, then by current p95.
 */
export type DebugDependencyDeploymentComparison = {
  current_deployment_id: string;
  current_deployment_tag?: string;
  current_commit_sha?: string;
  previous_deployment_id: string;
  previous_deployment_tag?: string;
  previous_commit_sha?: string;
  route?: string;
  dependencies: Array<DebugDependencyLatencyItem>;
  truncated: boolean;
};

