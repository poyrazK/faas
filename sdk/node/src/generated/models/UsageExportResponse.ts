/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One usage record: app id, GB-hours consumed, started/finished timestamps for the export window.
 */
export type UsageExportResponse = {
  app_id: string;
  month: string;
  mb_seconds: number;
  requests: number;
};

