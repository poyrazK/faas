/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Unweighted billable requests for one route over the requested period.
 */
export type APIConsumerRouteUsage = {
  /**
   * Method and route template. Empty for unattributed requests.
   */
  route: string;
  billable_requests: number;
};

