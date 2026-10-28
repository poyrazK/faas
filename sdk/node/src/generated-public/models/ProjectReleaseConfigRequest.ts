/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectReleaseSecretVersionMap } from './ProjectReleaseSecretVersionMap.js';
/**
 * Complete variable sets and optional staged secret version references for selected workloads; omitted snapshots inherit prior release values.
 */
export type ProjectReleaseConfigRequest = {
  secrets?: ProjectReleaseSecretVersionMap;
  env?: Record<string, Record<string, string>>;
};

