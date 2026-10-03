/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PreAuthPolicyObservation } from './PreAuthPolicyObservation.js';
/**
 * Observe-mode decisions and final response classes for an app's configured pre-auth policies.
 */
export type PreAuthObservationsResponse = {
  app_id: string;
  range: '5m' | '15m' | '1h' | '6h' | '24h' | '7d' | '15d';
  /**
   * prometheus or degraded: <reason>.
   */
  source: string;
  as_of: string;
  policies: Array<PreAuthPolicyObservation>;
};

