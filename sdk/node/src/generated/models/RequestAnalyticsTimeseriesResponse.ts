/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RequestAnalyticsTimeseriesPoint } from './RequestAnalyticsTimeseriesPoint.js';
/**
 * Zero-filled UTC hourly request analytics for
 * `GET /v1/apps/{slug}/analytics/timeseries`. The effective window is
 * half-open [from, until) and bounded by plan retention.
 *
 */
export type RequestAnalyticsTimeseriesResponse = {
  slug: string;
  /**
   * Effective series lookback after retention clamping.
   */
  since: string;
  /**
   * First instant represented by the hourly series.
   */
  from: string;
  /**
   * Exclusive end instant represented by the hourly series.
   */
  until: string;
  /**
   * Indicates the series start was limited by plan retention.
   */
  window_clamped: boolean;
  /**
   * UTC bucket size.
   */
  bucket: '1h';
  points: Array<RequestAnalyticsTimeseriesPoint>;
  /**
   * UTC time when the series response was assembled.
   */
  as_of: string;
};

