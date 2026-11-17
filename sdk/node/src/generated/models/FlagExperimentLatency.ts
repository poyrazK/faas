/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One variant's p95 latency, from a histogram with 5% buckets.
 */
export type FlagExperimentLatency = {
  variant: string;
  requests: number;
  p95_ms: number;
};

