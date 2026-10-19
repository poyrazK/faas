/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DeploymentDependencyPin } from './DeploymentDependencyPin.js';
/**
 * Build-time wait on the API deployments a guided service migration selected (ADR-1666). It is checked before cache lookup and slot allocation and never holds a build slot while waiting.
 */
export type DeploymentBuildDependencyGate = {
  dependencies: Array<DeploymentDependencyPin>;
  status: 'waiting' | 'ready' | 'failed';
  /**
   * Machine-readable reason while waiting or failed, for example build_dependency_not_ready or build_dependency_timeout. Absent once ready.
   */
  code?: string;
  /**
   * The dependency or deadline currently blocking the build.
   */
  blocker?: string;
  deadline_at: string;
  /**
   * Earliest time the gate is re-checked. Zero time (0001-01-01T00:00:00Z) when no re-check is scheduled.
   */
  check_after: string;
};

