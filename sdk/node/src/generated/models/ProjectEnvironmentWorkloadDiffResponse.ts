/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentBindingChangeResponse } from './ProjectEnvironmentBindingChangeResponse.js';
import type { ProjectEnvironmentReleaseDiffResponse } from './ProjectEnvironmentReleaseDiffResponse.js';
import type { ProjectEnvironmentSecretChangeResponse } from './ProjectEnvironmentSecretChangeResponse.js';
import type { ProjectEnvironmentVariableChangeResponse } from './ProjectEnvironmentVariableChangeResponse.js';
/**
 * Release, variable, secret, and binding changes for one workload.
 */
export type ProjectEnvironmentWorkloadDiffResponse = {
  workload_slug: string;
  workload_name: string;
  release: ProjectEnvironmentReleaseDiffResponse;
  variables: Array<ProjectEnvironmentVariableChangeResponse>;
  secrets: Array<ProjectEnvironmentSecretChangeResponse>;
  bindings: Array<ProjectEnvironmentBindingChangeResponse>;
};

