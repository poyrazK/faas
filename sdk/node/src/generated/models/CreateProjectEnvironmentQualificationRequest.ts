/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentQualificationCheck } from './ProjectEnvironmentQualificationCheck.js';
/**
 * Closed-schema health and smoke probe results for one exact active source release set, non-secret source configuration version, and per-workload secret revision fingerprints.
 */
export type CreateProjectEnvironmentQualificationRequest = {
  release_set_id: string;
  configuration_version: number;
  configuration_hash: string;
  secret_revision_hashes: Record<string, string>;
  checks: Array<ProjectEnvironmentQualificationCheck>;
};

