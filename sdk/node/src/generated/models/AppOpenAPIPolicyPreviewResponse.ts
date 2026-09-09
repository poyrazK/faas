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
   * preview, empty: no_import, or degraded: routes_unavailable.
   */
  source: string;
  /**
   * Whether the gatewayd observed-route bridge returned successfully.
   */
  observed_available: boolean;
  /**
   * OpenAPI version from the persisted declaration, when present.
   */
  openapi_version?: string;
  routes: Array<AppOpenAPIPolicyPreviewRoute>;
  suggestions?: Array<EdgeRuleSuggestion>;
};

