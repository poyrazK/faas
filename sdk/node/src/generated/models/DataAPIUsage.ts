/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataAPIUsageBucket } from './DataAPIUsageBucket.js';
import type { DataAPIUsageCounts } from './DataAPIUsageCounts.js';
import type { DataAPIUsageMonthToDate } from './DataAPIUsageMonthToDate.js';
/**
 * An app's reported Data API usage over a window, zero-filled per bucket.
 */
export type DataAPIUsage = {
  app: string;
  from: string;
  to: string;
  granularity: 'hour' | 'day';
  totals: DataAPIUsageCounts;
  month_to_date: DataAPIUsageMonthToDate;
  buckets: Array<DataAPIUsageBucket>;
};

