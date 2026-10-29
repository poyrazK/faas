/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteCustomerObservation } from './RouteCustomerObservation.js';
/**
 * Observed customer exposure for one exact route label and HTTP method on the selected deployment.
 */
export type RouteCustomerUsage = {
  /**
   * Exact recorded route label; may include its HTTP method prefix.
   */
  route: string;
  /**
   * HTTP method for this route observation.
   */
  method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'HEAD' | 'OPTIONS';
  /**
   * All weighted retained requests for this route and deployment.
   */
  requests: number;
  /**
   * Requests with at least one resolvable account-owned consumer or tenant identity.
   */
  identified_requests: number;
  /**
   * Requests with neither consumer nor tenant identity recorded.
   */
  anonymous_requests: number;
  /**
   * Requests with recorded identities that cannot be resolved within the app and account boundary.
   */
  unresolved_identity_requests: number;
  /**
   * Distinct observed app-owned consumers before detail caps; overlaps tenant count.
   */
  consumer_count: number;
  /**
   * Distinct observed account-owned request-time tenants before detail caps; overlaps consumer count.
   */
  platform_tenant_count: number;
  /**
   * Latest recorded timestamp for this route; may be a minute bucket.
   */
  last_observed_at: string;
  /**
   * Top recorded identity pairs by request count, with deterministic ID tie ordering.
   */
  customers: Array<RouteCustomerObservation>;
  /**
   * More identity pairs matched than the customer detail limit.
   */
  customers_truncated: boolean;
  /**
   * Identified requests belonging to identity pairs omitted by the detail cap.
   */
  other_customer_requests: number;
};

