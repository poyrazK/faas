/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Gates an automatic stage on a conversion event: customers the stage serves are compared with every other customer that evaluated the flag. The stage rolls back to 0 when their rate is clearly more than max_decrease lower, and advances only with min_units in each group and no such decrease.
 */
export type FlagRolloutMetric = {
  event: string;
  /**
   * Tolerated absolute conversion rate decrease; 0 means 0.01.
   */
  max_decrease?: number;
  /**
   * Customers each group needs before a stage advances; 0 means 100.
   */
  min_units?: number;
};

