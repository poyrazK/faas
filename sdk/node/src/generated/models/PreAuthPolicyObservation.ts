/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type PreAuthPolicyObservation = {
  /**
   * Bounded app, route_<index>, or failures_<index> policy identifier. Index is the route's current array position.
   */
  policy_id: string;
  kind: 'app' | 'route' | 'failures';
  /**
   * Configured public method; omitted for the app policy.
   */
  method?: string;
  /**
   * Configured public path; omitted for the app policy.
   */
  path?: string;
  would_block: number;
  result_2xx: number;
  result_3xx: number;
  result_4xx: number;
  result_5xx: number;
  result_unknown: number;
};

