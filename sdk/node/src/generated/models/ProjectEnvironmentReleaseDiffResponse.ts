/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentReleaseWorkloadResponse } from './ProjectEnvironmentReleaseWorkloadResponse.js';
/**
 * Before-and-after release state for a workload in an environment diff.
 */
export type ProjectEnvironmentReleaseDiffResponse = {
  kind: 'added' | 'removed' | 'changed' | 'unchanged';
  before: ProjectEnvironmentReleaseWorkloadResponse;
  after: ProjectEnvironmentReleaseWorkloadResponse;
};

