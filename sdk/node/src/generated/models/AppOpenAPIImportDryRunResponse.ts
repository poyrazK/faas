/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EdgeRuleSuggestion } from './EdgeRuleSuggestion.js';
/**
 * Response body for `POST /v1/apps/{slug}/openapi/dry-run`. Read-only; no persist,
 * no `pg_notify`, no MFA. Empty `suggestions` array when the
 * doc is fully covered by existing validate edge rules.
 *
 */
export type AppOpenAPIImportDryRunResponse = {
  /**
   * Suggested EdgeRuleSuggestion rows.
   */
  suggestions: Array<EdgeRuleSuggestion>;
  /**
   * OpenAPI version declared by the dry-run doc.
   */
  openapi_version: string;
  /**
   * Number of operations in the dry-run doc.
   */
  endpoint_count: number;
};

