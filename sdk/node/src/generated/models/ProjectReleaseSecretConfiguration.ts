/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectReleaseSecretVersionMap } from './ProjectReleaseSecretVersionMap.js';
/**
 * Candidate and baseline immutable secret version references bound to an atomic release.
 */
export type ProjectReleaseSecretConfiguration = {
  versions: ProjectReleaseSecretVersionMap;
  previous_versions: ProjectReleaseSecretVersionMap;
};

