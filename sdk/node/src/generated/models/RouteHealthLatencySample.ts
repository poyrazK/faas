/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Retained row counts and publisher weights. Span samples are actual retained spans, at most 100 per row. Guest p95 weights measured rows, including zero. Wake boot p95 counts each distinct wake once and requires scheduler events scoped to the app and recorded instance on the selected deployment. Missing stage evidence is omitted; incomplete timing suppresses exclusive percentiles.
 */
export type RouteHealthLatencySample = {
  sampled_rows: number;
  sampled_requests: number;
  samples_truncated: boolean;
  span_rows: number;
  missing_span_rows: number;
  span_samples: number;
  spans_truncated: boolean;
  timing_incomplete: boolean;
  guest_rows: number;
  guest_requests: number;
  cold_boot_requests: number;
  wake_samples: number;
  guest_p95_ms?: number;
  wake_boot_p95_ms?: number;
};

