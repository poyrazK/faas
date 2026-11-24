/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Cap on a consumer's requests on one route. Zero is unlimited; at least one limit is positive. Each admitted request counts one, whatever the route's rate-card weight.
 */
export type APIConsumerPlanRouteLimit = {
  /**
   * Requests per consumer per UTC minute on this route; over the limit the gateway returns 429.
   */
  max_requests_per_minute?: number;
  /**
   * Requests per consumer per UTC month on this route; over the limit the gateway returns 429 until the month ends.
   */
  max_requests_per_month?: number;
};

