/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentQueueBinding } from './ProjectEnvironmentQueueBinding.js';
/**
 * Complete stage queue replacement fenced by the workload revision, not the queue collection clock.
 */
export type ReplaceProjectEnvironmentQueueBindingsRequest = {
  expected_revision: number;
  bindings: Array<ProjectEnvironmentQueueBinding>;
};

