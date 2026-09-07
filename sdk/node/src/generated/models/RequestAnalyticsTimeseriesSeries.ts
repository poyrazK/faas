/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RequestAnalyticsTimeseriesPoint } from './RequestAnalyticsTimeseriesPoint.js';
export type RequestAnalyticsTimeseriesSeries = {
  value: string;
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'HEAD' | 'OPTIONS';
  points: Array<RequestAnalyticsTimeseriesPoint>;
};

