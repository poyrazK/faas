/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-route detail row (ADR-093). The `route` field is the
 * bounded label: `"GET /users/4f8a"` for an admitted route, or
 * `"__route_other__"` for the overflow bucket. Latency fields
 * are milliseconds over the full request duration (status-
 * agnostic — failures included). Same zero-on-degraded
 * contract as `AppMetricsResponse`.
 *
 */
export type RouteRow = {
  /**
   * Bounded route label (method + raw path, or `__route_other__`).
   */
  route: string;
  /**
   * Number of requests with this route in the window.
   */
  count: number;
  /**
   * p50 of `gateway_request_duration_seconds_bucket{app, route, class}` over the window, in ms.
   */
  p50_ms: number;
  /**
   * p95 over all classes in the window, in ms.
   */
  p95_ms: number;
  /**
   * p99 over all classes in the window, in ms.
   */
  p99_ms: number;
  /**
   * Share of 5xx requests with this route in the window. Client-caused 4xx responses do not consume availability budget.
   */
  error_pct: number;
};

