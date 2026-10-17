/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { HostingRouteCheckResult } from './HostingRouteCheckResult.js';
/**
 * API route checks from the app's imported OpenAPI document. document_sha256 binds them to that exact document.
 */
export type HostingRouteCheckSet = {
  source: 'app_openapi';
  document_sha256?: string;
  status: 'verified' | 'failed' | 'unavailable';
  checks: Array<HostingRouteCheckResult>;
};

