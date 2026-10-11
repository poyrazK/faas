/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowDependencyFinding } from './OperationWorkflowDependencyFinding.js';
/**
 * Bounded deterministic depth-first traversal of unmet reported prerequisites within one customer/application/environment. Unknown retained state and traversal limits are distinct. Terminal sources and satisfied requirements stop traversal. Current reports may change while reading.
 */
export type OperationWorkflowDependencyTrace = {
  findings: Array<OperationWorkflowDependencyFinding>;
  /**
   * Distinct inspected workflow references including the selected source and cached lookups of unknown or satisfied prerequisites.
   */
  visited_workflow_count: number;
  examined_dependency_count: number;
  depth_limit: 8;
  workflow_limit: 64;
  finding_limit: 128;
  dependency_limit: 256;
  truncated: boolean;
  limits_reached?: Array<'depth' | 'workflows' | 'findings' | 'dependencies'>;
};

