/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectSummaryResponse } from './ProjectSummaryResponse.js';
import type { ProjectWorkloadResponse } from './ProjectWorkloadResponse.js';
/**
 * Workloads and related resources that remain live when a project is deleted.
 */
export type ProjectDeletePreviewResponse = {
  project: ProjectSummaryResponse;
  workloads: Array<ProjectWorkloadResponse>;
  domain_count: number;
  env_count: number;
  cron_count: number;
};

