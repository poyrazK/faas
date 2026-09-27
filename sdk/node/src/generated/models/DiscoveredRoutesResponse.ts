/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DiscoveredAPIRoute } from './DiscoveredAPIRoute.js';
/**
 * Bounded API route inventory independent of exact audit retention.
 */
export type DiscoveredRoutesResponse = {
  app_id: string;
  routes: Array<DiscoveredAPIRoute>;
  /**
   * True once new candidates overflow the 500-route cap.
   */
  cap_hit: boolean;
  source: 'usage_outbox';
};

