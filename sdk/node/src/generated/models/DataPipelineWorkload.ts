/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataPipelineWorkWindow } from './DataPipelineWorkWindow.js';
/**
 * Owner maintenance budgets within hard pilot bounds. Query timeout is zero for the default or 50 through 2000 milliseconds; batch count is zero for default or one through four. Native COPY bandwidth is outside this policy.
 */
export type DataPipelineWorkload = {
  window?: DataPipelineWorkWindow;
  query_timeout_millis?: number;
  batches_per_tick?: number;
};

