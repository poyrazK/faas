/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Sampled aggregate of platform-classified dependency spans observed under one route. Exclusive duration subtracts overlapping direct child spans; it approximates dependency-owned wait and is not additive request latency. Calls are weighted by collapsed request count and are not a complete span call count.
 */
export type RequestAnalyticsDependency = {
  /**
   * Allowlisted dependency category.
   */
  type: string;
  /**
   * Allowlisted dependency kind when available.
   */
  kind?: string;
  /**
   * Redacted span name.
   */
  name: string;
  /**
   * Number of retained span observations.
   */
  samples: number;
  /**
   * Observed span sample count weighted by the collapsed request-row count; not a full call count.
   */
  calls: number;
  error_calls: number;
  /**
   * Weighted p95 inclusive span duration.
   */
  p95_ms: number;
  /**
   * Weighted p95 span duration excluding overlapping direct child spans.
   */
  exclusive_p95_ms: number;
};

