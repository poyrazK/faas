/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataAPIUsageHour } from './DataAPIUsageHour.js';
/**
 * Private versioned runtime usage report. A report id names the reporting instance and process and is applied once; hours are unique.
 */
export type DataAPIUsageReport = {
  version: 1;
  report_id: string;
  hours: Array<DataAPIUsageHour>;
};

