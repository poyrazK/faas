/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One typed reference to a signal. app_metric and custom_metric use metric, log_count uses query, slo uses slo_id.
 */
export type DashboardWidget = {
  title: string;
  kind: 'app_metric' | 'log_count' | 'custom_metric' | 'slo';
  /**
   * Slug of an app the account owns.
   */
  app: string;
  /**
   * app_metric: request_count, error_rate_pct, latency_p50_ms, latency_p95_ms, latency_p99_ms or cold_start_pct. custom_metric: the pushed metric name.
   */
  metric?: string;
  /**
   * log_count: log search filters as a URL query string (source, level, status, route, deployment_id, request_id, trace_id, q).
   */
  query?: string;
  /**
   * slo: the SLO id.
   */
  slo_id?: string;
  /**
   * Defaults to 1h; ignored by slo widgets.
   */
  window?: '5m' | '1h' | '6h' | '24h' | '7d';
};

