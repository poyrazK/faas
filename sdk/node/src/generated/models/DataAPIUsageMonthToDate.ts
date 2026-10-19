/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Admitted requests in the current UTC calendar month, all and anonymous, which owner caps compare against.
 */
export type DataAPIUsageMonthToDate = {
  /**
   * UTC calendar month as YYYY-MM.
   */
  month: string;
  requests: number;
  anonymous_requests: number;
};

