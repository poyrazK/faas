/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentQueueBinding } from './ProjectEnvironmentQueueBinding.js';
/**
 * Desired stage queue definitions. Consumer activation is currently unavailable and these definitions do not enable delivery or qualify promotion.
 */
export type ProjectEnvironmentQueueBindingsResponse = {
  environment: string;
  workload: string;
  /**
   * Queue collection clock.
   */
  revision: number;
  /**
   * Complete workload revision to use as expected_revision on replacement.
   */
  workload_revision: number;
  config_hash: string;
  activation_state: 'unavailable';
  bindings: Array<ProjectEnvironmentQueueBinding>;
};

