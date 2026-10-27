/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RequestAnalyticsTimeseriesPoint } from './RequestAnalyticsTimeseriesPoint.js';
/**
 * A grouped request analytics series with its hourly points.
 */
export type RequestAnalyticsTimeseriesSeries = {
  value: string;
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'HEAD' | 'OPTIONS';
  points: Array<RequestAnalyticsTimeseriesPoint>;
};

