/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A saved events insight: the event series query and the measure its value totals.
 */
export type InsightEventsQuery = {
  event: string;
  where?: Record<string, string>;
  interval?: 'day' | 'hour';
  unit?: 'customer' | 'subject';
  /**
   * Breakdown of the series: flag:KEY or property:KEY.
   */
  by?: string;
  /**
   * What the insight's value totals.
   */
  measure?: 'events' | 'value';
};

