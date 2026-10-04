/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Weighted requests and responses for one watched HTTP code in one deployment/window. Rate is responses divided by requests, zero when requests are zero.
 */
export type RouteHealthStatusCounts = {
  requests: number;
  responses: number;
  rate: number;
};

