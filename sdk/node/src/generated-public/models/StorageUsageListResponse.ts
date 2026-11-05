/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { StorageUsageResponse } from './StorageUsageResponse.js';
/**
 * Page shape for GET /v1/usage/storage — wraps an array of per-app storage rollup rows.
 */
export type StorageUsageListResponse = {
  items: Array<StorageUsageResponse>;
};

