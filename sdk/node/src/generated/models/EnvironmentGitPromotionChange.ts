/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EnvironmentWorkloadSource } from './EnvironmentWorkloadSource.js';
/**
 * Only a workload source contract changes; target configuration and identity are preserved.
 */
export type EnvironmentGitPromotionChange = {
  /**
   * Target workload resource whose source contract is proposed to change.
   */
  resource: string;
  before: EnvironmentWorkloadSource;
  after: EnvironmentWorkloadSource;
};

