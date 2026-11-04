/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PlanWorkload } from './PlanWorkload.js';
/**
 * Planned workload and provided key names used by metadata-only deployment readiness checks.
 */
export type ProjectDeploymentCheckWorkload = {
  workload: PlanWorkload;
  command_shell?: boolean;
  release_command?: Array<string>;
  release_command_shell?: boolean;
  /**
   * Key names proposed by a separate secrets input; values are never included.
   */
  provided_keys?: Array<string>;
};

