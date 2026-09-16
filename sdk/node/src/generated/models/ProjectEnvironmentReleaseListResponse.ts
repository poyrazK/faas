/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentReleaseWorkloadResponse } from './ProjectEnvironmentReleaseWorkloadResponse.js';
/**
 * Current non-secret release inventory for a project environment.
 */
export type ProjectEnvironmentReleaseListResponse = {
  project_slug: string;
  environment: string;
  workloads: Array<ProjectEnvironmentReleaseWorkloadResponse>;
};

