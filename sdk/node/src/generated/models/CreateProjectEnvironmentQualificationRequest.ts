/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentQualificationCheck } from './ProjectEnvironmentQualificationCheck.js';
/**
 * Closed-schema health and smoke probe results for one exact active source release set, non-secret source configuration version, and workload and shared-secret revision fingerprints observed before probes.
 */
export type CreateProjectEnvironmentQualificationRequest = {
  /**
   * Configuration fingerprints observed before probes; environment-owned settings require an exact match to deployed revisions.
   */
  workload_config_hashes?: Record<string, string>;
  release_set_id: string;
  configuration_version: number;
  configuration_hash: string;
  /**
   * Per-workload secret fingerprints plus the reserved project:secrets identity from the pre-probe environment state. The shared identity is required once the environment has held shared secrets, including after the final key is deleted.
   */
  secret_revision_hashes: Record<string, string>;
  checks: Array<ProjectEnvironmentQualificationCheck>;
};

