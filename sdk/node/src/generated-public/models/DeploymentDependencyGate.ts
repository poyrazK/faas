/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DeploymentDependencyPin } from './DeploymentDependencyPin.js';
/**
 * Compose dependency readiness gate pinned at admission. The deadline starts when the candidate first presents its own readiness proof.
 */
export type DeploymentDependencyGate = {
  dependencies: Array<DeploymentDependencyPin>;
  started_at?: string;
  deadline_at?: string;
  status: 'waiting' | 'ready' | 'failed';
  /**
   * The dependency or deadline currently blocking release.
   */
  blocker?: string;
};

