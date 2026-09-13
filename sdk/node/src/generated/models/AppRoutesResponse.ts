/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Fleet-wide per-route label snapshot (ADR-093). The control-plane
 * Prometheus instance aggregates every active, scrape-ready compute
 * gateway; collector counts distinguish complete, partial, and
 * unavailable observations from a healthy no-traffic result.
 * Each item is `"<METHOD> <PATH>"` (pre-edge-rule-rewrite) for
 * an admitted route, or the reserved `"__route_other__"` overflow
 * bucket label. The fleet union is bounded again at 50 distinct real
 * routes plus the reserved overflow bucket (ADR-093 D2).
 *
 * `cap_hit` is true when a compute collector emitted the overflow
 * bucket or the fleet union reached the 50-route bound. It is false
 * on `source: unavailable`, where cap state is unknown.
 *
 */
export type AppRoutesResponse = {
  slug: string;
  app_id?: string;
  routes: Array<string>;
  source: 'live' | 'partial' | 'unavailable';
  /**
   * Active scrape-ready compute route collectors in the registry.
   */
  collectors_expected: number;
  /**
   * Expected collectors with a current successful Prometheus scrape.
   */
  collectors_healthy: number;
  /**
   * True when the fleet route union reaches `RouteMetricsPerAppCap`
   * (50) or a collector reports `__route_other__`. False on
   * `source: unavailable`, where cap state is unknown.
   *
   */
  cap_hit: boolean;
};

