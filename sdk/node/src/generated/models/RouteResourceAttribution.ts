/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One route's share of the window's memory and CPU peaks.
 */
export type RouteResourceAttribution = {
  route: string;
  method: string;
  requests: number;
  /**
   * Time requests of the route were in flight, summed over concurrent requests.
   */
  busy_ms: number;
  active_minutes: number;
  /**
   * Share of active minutes the route ran in.
   */
  presence_share: number;
  high_memory_minutes: number;
  /**
   * Share of attributed memory peaks the route was in flight for.
   */
  high_memory_share: number;
  /**
   * Memory peaks the route was the only route in flight for.
   */
  sole_high_memory_minutes: number;
  mem_peak_p95_with_mb: number;
  /**
   * p95 memory peak in active minutes without the route.
   */
  mem_peak_p95_without_mb: number;
  mem_rise_p95_with_mb: number;
  mem_rise_p95_without_mb: number;
  high_cpu_minutes: number;
  high_cpu_share: number;
  cpu_peak_p95_with_millicores: number;
  cpu_peak_p95_without_millicores: number;
};

