/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Daily UTC minute window with an inclusive start and exclusive end. Start and end differ; a start after end crosses midnight. Omit the window for all-day work.
 */
export type DataPipelineWorkWindow = {
  start_minute_utc: number;
  end_minute_utc: number;
};

