/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One server-side probe aggregation bucket. Percentiles are omitted when every probe in the bucket failed.
 */
export type DataUpstreamHistoryBucket = {
  /**
   * UTC start of the aggregation bucket.
   */
  sampled_at: string;
  /**
   * Successful probe RTT p50 in milliseconds.
   */
  p50_ms?: number | null;
  /**
   * Successful probe RTT p95 in milliseconds.
   */
  p95_ms?: number | null;
  /**
   * Total probes in the bucket, including failures.
   */
  sample_count: number;
};

