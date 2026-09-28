/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentDomainResponse } from './ProjectEnvironmentDomainResponse.js';
/**
 * Environment-owned hostname changes for one workload.
 */
export type ProjectEnvironmentDomainDiffResponse = {
  kind: 'unchanged' | 'changed';
  before: Array<ProjectEnvironmentDomainResponse>;
  after: Array<ProjectEnvironmentDomainResponse>;
};

