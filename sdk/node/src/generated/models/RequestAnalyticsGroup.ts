/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Bounded aggregate for a selected analytics dimension. Value is a route, country, hostname, normalized client family, status code, stable consumer UUID, __anonymous__, or __other__.
 */
export type RequestAnalyticsGroup = {
  value: string;
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'HEAD' | 'OPTIONS';
  requests: number;
  error_requests: number;
  error_rate_pct: number;
  cold_boots: number;
  p50_ms: number;
  p95_ms: number;
  p99_ms: number;
  cold_request_p95_ms?: number | null;
  wake_boot_p95_ms?: number | null;
  guest_execution_p50_ms?: number | null;
  guest_execution_p95_ms?: number | null;
  guest_cpu_avg_ms?: number | null;
  guest_cpu_p95_ms?: number | null;
  guest_peak_rss_max_mb?: number | null;
};

