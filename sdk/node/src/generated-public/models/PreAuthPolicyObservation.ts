/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Counts for one bounded app, route, failed-response, or target-observation policy slot.
 */
export type PreAuthPolicyObservation = {
  /**
   * Bounded app, route_<index>, failures_<index>, or targets_<index> policy identifier. Index is the route's current array position.
   */
  policy_id: string;
  kind: 'app' | 'route' | 'failures' | 'targets';
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
  /**
   * Selected app failures carrying a valid target identifier.
   */
  target_failures?: number;
  /**
   * Failures for which both bounded target shards exceeded the configured failed-response budget. Approximate signal; never blocks.
   */
  target_threshold?: number;
  /**
   * Selected app failures without a target header.
   */
  target_missing?: number;
  /**
   * Selected app failures with an invalid target header.
   */
  target_invalid?: number;
  /**
   * Valid target failures observed with process-local fallback because central coordination was unavailable.
   */
  target_fallback?: number;
};

