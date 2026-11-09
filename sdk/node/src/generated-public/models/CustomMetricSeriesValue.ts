/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One labelled series of a custom metric (from OTLP data point attributes).
 */
export type CustomMetricSeriesValue = {
  /**
   * Canonical k=v,... form, keys sorted; pass it as labels to the history endpoint.
   */
  key: string;
  labels: Record<string, string>;
  value: number;
  observed_at: string;
  stale: boolean;
};

