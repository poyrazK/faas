/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentReleaseWorkloadResponse } from './ProjectEnvironmentReleaseWorkloadResponse.js';
export type ProjectEnvironmentReleaseDiffResponse = {
  kind: 'added' | 'removed' | 'changed' | 'unchanged';
  before: ProjectEnvironmentReleaseWorkloadResponse;
  after: ProjectEnvironmentReleaseWorkloadResponse;
};

