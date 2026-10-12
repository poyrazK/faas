/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPTaskHandlerDeploymentWorkers } from './MCPTaskHandlerDeploymentWorkers.js';
/**
 * Outstanding task counts and live worker coverage for one tool and handler version.
 */
export type MCPTaskHandlerCompatibility = {
  tool_name: string;
  handler_version: string;
  waiting_tasks: number;
  live_compatible_workers: number;
  /**
   * Workers whose deployment identity is not available yet, including workers from older starter versions.
   */
  unknown_deployment_workers?: number;
  /**
   * Per-deployment worker counts. A report contains at most 768 deployment entries overall; larger snapshots set truncated=true.
   */
  deployments?: Array<MCPTaskHandlerDeploymentWorkers>;
};

