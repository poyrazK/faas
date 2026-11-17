/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CustomMetricSeriesValue } from './CustomMetricSeriesValue.js';
/**
 * One stored gauge.
 */
export type CustomMetricResponse = {
  name: string;
  value: number;
  observed_at: string;
  /**
   * True when this row is older than the freshness window and is therefore NOT driving scaling. Computed server-side so the API and the scheduler cannot disagree about which rows count.
   */
  stale: boolean;
  /**
   * gauge for pushed values (a scaling signal); counter for OTLP cumulative sums, which never drive scaling.
   */
  kind?: 'gauge' | 'counter';
  /**
   * A labelled metric's series. value is then their aggregate: the sum for counters, the maximum of fresh series for gauges. Absent for an unlabelled metric.
   */
  series?: Array<CustomMetricSeriesValue>;
};

