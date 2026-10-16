/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectDeploymentCheck } from './ProjectDeploymentCheck.js';
export type ProjectDeploymentReadiness = {
  schema_version: 1;
  project_slug: string;
  environment: string;
  from_environment?: string;
  status: 'ready' | 'blocked' | 'unknown';
  state_hash?: string;
  checked_at: string;
  runtime_checked: boolean;
  checks: Array<ProjectDeploymentCheck>;
};

