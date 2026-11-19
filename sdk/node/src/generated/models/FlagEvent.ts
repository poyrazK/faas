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
  /**
   * Optional end-user subject of the request; only its hash is stored.
   */
  subject?: string;
  /**
   * Optional string properties; keys match ^[a-z][a-z0-9_]{0,63}$, values are at most 256 bytes.
   */
  properties?: Record<string, string>;
  /**
   * Served value, as a string, of each flag the request evaluated before the event.
   */
  flags?: Record<string, string>;
};

