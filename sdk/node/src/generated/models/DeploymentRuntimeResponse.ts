/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RuntimeReleaseResponse } from './RuntimeReleaseResponse.js';
/**
 * Recorded deployment runtime binding and published same-family base candidates.
 */
export type DeploymentRuntimeResponse = {
  deployment_id: string;
  status: 'pinned' | 'unknown' | 'unsupported';
  reason: string;
  current: (RuntimeReleaseResponse | null);
  releases: Array<RuntimeReleaseResponse>;
};

