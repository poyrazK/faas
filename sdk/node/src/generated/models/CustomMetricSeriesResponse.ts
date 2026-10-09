/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * ADR-745 history of one pushed custom metric. Degraded responses carry null points.
 */
export type CustomMetricSeriesResponse = {
  app_id: string;
  name: string;
  /**
   * counter histories are a per-second rate of the cumulative count.
   */
  kind: 'gauge' | 'counter';
  range: '1h' | '6h' | '24h' | '7d' | '15d';
  /**
   * Prometheus step between points.
   */
  step: string;
  /**
   * "prometheus" or "degraded: <reason>".
   */
  source: string;
  points: any[] | null;
};

