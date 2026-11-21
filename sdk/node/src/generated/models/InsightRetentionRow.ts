/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A retention cohort, breakdown value or the overall row.
 */
export type InsightRetentionRow = {
  /**
   * Cohort start; absent on the overall and breakdown rows.
   */
  start?: string;
  /**
   * Breakdown value.
   */
  value?: string;
  units: number;
  /**
   * Units whose period k has ended (index k-1).
   */
  eligible: Array<number>;
  returned: Array<number>;
  rates: Array<number>;
};

