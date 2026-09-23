/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentBindingResponse } from './ProjectEnvironmentBindingResponse.js';
import type { ProjectEnvironmentReleaseWorkloadResponse } from './ProjectEnvironmentReleaseWorkloadResponse.js';
import type { ProjectEnvironmentSecretResponse } from './ProjectEnvironmentSecretResponse.js';
import type { ProjectEnvironmentVariableResponse } from './ProjectEnvironmentVariableResponse.js';
export type ProjectEnvironmentStateWorkloadResponse = {
  workload_slug: string;
  workload_name: string;
  release: ProjectEnvironmentReleaseWorkloadResponse;
  variables: Array<ProjectEnvironmentVariableResponse>;
  secrets: Array<ProjectEnvironmentSecretResponse>;
  bindings: Array<ProjectEnvironmentBindingResponse>;
};

