/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Awake time one non-request cause explains (ADR-1199).
 */
export type RouteAwakeSource = {
  /**
   * route_probe: synthetic canary probes the app answered (ADR-847; kept for a day). prewarm: instances a scheduled prewarm admitted.
   */
  source: 'route_probe' | 'prewarm';
  /**
   * Probe requests answered without a 5xx or an auth rejection, or prewarmed instances.
   */
  events: number;
  awake_seconds: number;
  awake_share: number;
  gb_hours: number;
};

