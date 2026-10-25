/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CronRun } from './CronRun.js';
/**
 * Page of cron runs; ordered newest-first. Pass the LAST id of the returned slice as the next `?before=` to load older runs.
 */
export type ListCronRunsResponse = {
  runs: Array<CronRun>;
};

