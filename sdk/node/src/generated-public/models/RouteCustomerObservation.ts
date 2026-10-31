/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One request-time consumer and tenant identity group; either or both IDs may be present.
 */
export type RouteCustomerObservation = {
  /**
   * Stable authenticated consumer ID owned by the selected app, if recorded and resolvable.
   */
  consumer_id?: string;
  /**
   * Account-owned tenant ID recorded when these requests occurred, if resolvable.
   */
  platform_tenant_id?: string;
  /**
   * Weighted retained requests attributed to this identity pair.
   */
  requests: number;
  /**
   * Latest recorded telemetry timestamp for this identity group; may be a minute bucket.
   */
  last_observed_at: string;
};

