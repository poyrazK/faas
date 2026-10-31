/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataUpstreamResponse } from './DataUpstreamResponse.js';
/**
 * List response wrapper. `quota_max` is the per-plan cap; `count` is the
 * number of rows in `upstreams` (may be less than `quota_max`).
 *
 */
export type DataUpstreamListResponse = {
  upstreams: Array<DataUpstreamResponse>;
  quota_max: number;
  count: number;
};

