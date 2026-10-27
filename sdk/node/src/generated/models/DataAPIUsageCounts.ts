/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Data API usage counts. Admitted requests by access kind, gateway and engine rejections (4xx), errors (5xx), requests refused by owner caps, rows the engine reported and response bytes. Owner telemetry from the app's runtime, not billing records.
 */
export type DataAPIUsageCounts = {
  user_requests: number;
  anonymous_requests: number;
  server_requests: number;
  rejected_requests: number;
  error_requests: number;
  capped_requests: number;
  rows_returned: number;
  response_bytes: number;
};

