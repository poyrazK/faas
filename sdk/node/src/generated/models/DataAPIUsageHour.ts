/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataAPIUsageCounts } from './DataAPIUsageCounts.js';
/**
 * One UTC hour's usage delta in a runtime report.
 */
export type DataAPIUsageHour = (DataAPIUsageCounts & {
  /**
   * UTC hour the delta belongs to; aligned to the hour.
   */
  hour: string;
});

