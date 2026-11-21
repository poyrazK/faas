/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One time bucket and breakdown value of an event series.
 */
export type InsightEventPoint = {
  start: string;
  /**
   * Breakdown value; (none) without the flag or property, (other) past the breakdown bound.
   */
  breakdown?: string;
  events: number;
  /**
   * Distinct customers or subjects; for (other) a sum that may count a unit more than once.
   */
  units: number;
  value: number;
};

