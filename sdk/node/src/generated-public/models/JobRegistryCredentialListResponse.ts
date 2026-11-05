/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { JobRegistryCredentialResponse } from './JobRegistryCredentialResponse.js';
/**
 * Job registry credential metadata plus the per-job quota.
 */
export type JobRegistryCredentialListResponse = {
  credentials: Array<JobRegistryCredentialResponse>;
  quota_max: number;
  count: number;
};

