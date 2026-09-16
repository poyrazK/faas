/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentConfigChange } from './ProjectEnvironmentConfigChange.js';
/**
 * Stable key-level diff between two project environment configuration snapshots.
 */
export type ProjectEnvironmentConfigDiffResponse = {
  project_slug: string;
  from_environment: string;
  to_environment: string;
  from_version: number;
  to_version: number;
  from_hash: string;
  to_hash: string;
  changes: Array<ProjectEnvironmentConfigChange>;
};

