/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppHealthRequestPolicy } from './AppHealthRequestPolicy.js';
/**
 * Scoped request evidence; counts are confirmed only when known is true. Covers current serving releases, excluding previous releases and other scopes.
 */
export type AppHealthRequests = {
  known: boolean;
  coverage: 'serving_deployments';
  window_seconds: number;
  deployment_ids: Array<string>;
  request_count: number;
  server_errors: number;
  error_rate_pct: number;
  policy: AppHealthRequestPolicy;
};

