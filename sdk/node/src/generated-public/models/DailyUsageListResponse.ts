/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DailyUsageResponse } from './DailyUsageResponse.js';
/**
 * Page shape for GET /v1/usage/daily — wraps an array of per-app daily rollup rows.
 */
export type DailyUsageListResponse = {
  items: Array<DailyUsageResponse>;
};

