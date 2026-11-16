/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One conversion event a runtime SDK reports for experiments.
 */
export type FlagEvent = {
  /**
   * Conversion event name.
   */
  name: string;
  /**
   * Verified customer (platform tenant) of the request the event belongs to.
   */
  customer_id: string;
  /**
   * Optional integer value, such as cents.
   */
  value?: number;
  /**
   * When the event happened; within the last 24 hours. Defaults to now.
   */
  at?: string;
};

