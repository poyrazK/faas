/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppOpenAPIPolicyPreviewRoute } from './AppOpenAPIPolicyPreviewRoute.js';
import type { EdgeRuleSuggestion } from './EdgeRuleSuggestion.js';
/**
 * Read-only declared-vs-observed route and edge-policy preview
 * (API-hosting roadmap item 11 / ADR-126 follow-up). The response
 * never writes an OpenAPI document or edge rule.
 *
 */
export type AppOpenAPIPolicyPreviewResponse = {
  app_id: string;
  /**
   * preview, empty: no_import, degraded: routes_partial, or degraded: routes_unavailable.
   */
  source: string;
  /**
   * Whether current route telemetry or a persisted discovered-route inventory is available.
   */
  observed_available: boolean;
  /**
   * Completeness of the current fleet-wide telemetry input; persisted inventory availability is reported separately.
   */
  observed_source: 'live' | 'partial' | 'unavailable';
  /**
   * Whether durable, opt-in discovered routes contributed to the observed route set.
   */
  observed_inventory_available: boolean;
  /**
   * Whether the live route snapshot or durable discovered-route inventory reached its route cap.
   */
  observed_cap_hit: boolean;
  /**
   * Number of registry compute gateways expected to contribute route observations.
   */
  collectors_expected: number;
  /**
   * Expected compute route collectors currently healthy in Prometheus.
   */
  collectors_healthy: number;
  /**
   * OpenAPI version from the persisted declaration, when present.
   */
  openapi_version?: string;
  routes: Array<AppOpenAPIPolicyPreviewRoute>;
  suggestions?: Array<EdgeRuleSuggestion>;
};

