/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Accepted and dropped events of a batch, with the month's event usage.
 */
export type FlagEventsResponse = {
  accepted: number;
  /**
   * Exposures that matched a running subject experiment of an active customer.
   */
  exposures?: number;
  dropped: {
    /**
     * Over the monthly event quota.
     */
    quota: number;
    /**
     * Not an active platform tenant of the account.
     */
    unknown_customer: number;
    /**
     * A new event name over the environment's name bound.
     */
    name_limit: number;
    /**
     * Invalid name or value, or a time outside the accepted window.
     */
    invalid: number;
  };
  month: {
    limit: number;
    accepted: number;
    dropped: number;
  };
};

