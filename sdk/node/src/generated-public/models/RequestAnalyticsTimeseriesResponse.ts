/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RequestAnalyticsTelemetryGap } from './RequestAnalyticsTelemetryGap.js';
import type { RequestAnalyticsTimeseriesPoint } from './RequestAnalyticsTimeseriesPoint.js';
import type { RequestAnalyticsTimeseriesSeries } from './RequestAnalyticsTimeseriesSeries.js';
/**
 * Zero-filled UTC hourly request analytics for
 * `GET /v1/apps/{slug}/analytics/timeseries`. The effective window is
 * half-open [from, until) and bounded by plan retention.
 *
 */
export type RequestAnalyticsTimeseriesResponse = {
  slug: string;
  /**
   * Exact bounded route-label filter when a route-level series was requested.
   */
  route?: string;
  /**
   * Exact method filter when a route-level series was requested.
   */
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'HEAD' | 'OPTIONS' | 'OTHER';
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
  group_by?: 'route' | 'country' | 'referrer_host' | 'ua_family' | 'status' | 'consumer_id';
  series?: Array<RequestAnalyticsTimeseriesSeries>;
  /**
   * UTC time when the series response was assembled.
   */
  as_of: string;
  /**
   * Same as on the analytics overview: true when every gateway delivered the series window, judged up to two minutes before `as_of`.
   */
  telemetry_complete?: boolean;
  /**
   * Why requests in the series window may be missing. Empty or absent when the window is complete.
   */
  telemetry_gaps?: Array<RequestAnalyticsTelemetryGap>;
};

