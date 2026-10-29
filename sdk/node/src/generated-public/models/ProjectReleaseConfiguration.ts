/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectReleaseSecretConfiguration } from './ProjectReleaseSecretConfiguration.js';
/**
 * Paired release configuration fingerprints and committed version identities; values remain private to server storage.
 */
export type ProjectReleaseConfiguration = {
  secrets?: ProjectReleaseSecretConfiguration;
  previous_version: number;
  previous_hash: string;
  target_hash: string;
  environment_hash: string;
  request_hash?: string;
  target_version?: number;
  restored_version?: number;
};

