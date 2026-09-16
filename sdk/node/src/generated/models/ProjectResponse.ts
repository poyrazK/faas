/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectSummaryResponse } from './ProjectSummaryResponse.js';
import type { ProjectWorkloadResponse } from './ProjectWorkloadResponse.js';
/**
 * Project metadata with its workloads, exclusions, and latest reconciliation state.
 */
export type ProjectResponse = (ProjectSummaryResponse & {
  workloads: Array<ProjectWorkloadResponse>;
  exclusions: Array<string>;
  last_reconciliation_status?: string;
  last_build_status?: string;
});

