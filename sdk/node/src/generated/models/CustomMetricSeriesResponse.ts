/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * History of one pushed custom metric. Degraded responses carry null points.
 */
export type CustomMetricSeriesResponse = {
  app_id: string;
  name: string;
  /**
   * counter histories are a per-second rate of the cumulative count.
   */
  kind: 'gauge' | 'counter';
  /**
   * The series key this history is for; absent for the metric's aggregate.
   */
  labels?: string;
  range: '1h' | '6h' | '24h' | '7d' | '15d';
  /**
   * Prometheus step between points.
   */
  step: string;
  /**
   * "prometheus", or "degraded: <reason>" when the metric history could not be read.
   */
  source: string;
  points: any[] | null;
};

