/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Daily platform-availability point. Successful and total count available and observed complete five-minute platform intervals; customer workload outcomes are excluded.
 */
export type StatusUptimeBucket = {
  date: string;
  uptime_pct: number;
  /**
   * Complete five-minute platform intervals that were available.
   */
  successful: number;
  /**
   * Complete five-minute platform intervals with telemetry.
   */
  total: number;
};

