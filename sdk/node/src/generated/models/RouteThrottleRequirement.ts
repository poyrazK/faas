/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Desired throttle key dimension, optional rate ceiling, and missing-identity behavior.
 */
export type RouteThrottleRequirement = {
  key_by: 'none' | 'api_key' | 'consumer_id' | 'jwt_subject' | 'country' | 'ip';
  max_rps?: number;
  missing_key_policy?: 'shared' | 'reject';
};

