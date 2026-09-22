/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One stored ADR-202 gauge.
 */
export type CustomMetricResponse = {
  name: string;
  value: number;
  observed_at: string;
  /**
   * True when this row is older than the freshness window and is therefore NOT driving scaling. Computed server-side so the API and the scheduler cannot disagree about which rows count.
   */
  stale: boolean;
};

