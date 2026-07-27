/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-app usage for one month: GB-hours consumed and a `next_before` cursor for paging deployments within the window.
 */
export type UsageResponse = {
  app_id: string;
  mb_seconds: number;
  requests: number;
  included_gb_hours: number;
};

