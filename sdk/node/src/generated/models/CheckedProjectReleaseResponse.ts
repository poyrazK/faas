/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BindingCheckReport } from './BindingCheckReport.js';
import type { ProjectReleaseSetResponse } from './ProjectReleaseSetResponse.js';
/**
 * Result of a checked project release operation with per-member binding checks.
 */
export type CheckedProjectReleaseResponse = {
  release: ProjectReleaseSetResponse;
  bindings_checks: Record<string, BindingCheckReport>;
};

