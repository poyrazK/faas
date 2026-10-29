/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CorsPresetResponse } from './CorsPresetResponse.js';
/**
 * GET /v1/cors-presets list shape. The (account-wide,
 * app-scoped) order mirrors ListCorsPresetsForAccount:
 * account-wide rows first (app_id IS NULL), then
 * app-scoped rows, both ordered by name.
 *
 */
export type CorsPresetListResponse = {
  presets: Array<CorsPresetResponse>;
};

