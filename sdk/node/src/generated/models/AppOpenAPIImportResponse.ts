/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Response body for `POST /v1/apps/{slug}/openapi`. One row per app in `app_openapi_docs`,
 * last-write-wins. Source is always `manual_import` — cold-
 * boot captures go to `deployment_openapi_docs` (item #1).
 * `endpoint_count` is the number of HTTP operations in the
 * imported doc's `paths.*`; `byte_size` is the raw body size
 * the handler enforced against
 * `state.OpenAPIImportMaxDocBytes` (256 KiB).
 *
 */
export type AppOpenAPIImportResponse = {
  /**
   * App UUID the import row is bound to.
   */
  app_id: string;
  /**
   * Row source. Always `manual_import` for this endpoint.
   */
  source: 'manual_import';
  /**
   * OpenAPI spec version the imported doc declares.
   */
  openapi_version: '3.0.0' | '3.0.1' | '3.0.2' | '3.0.3' | '3.0.4' | '3.1.0' | '3.1.1';
  /**
   * Number of HTTP operations in the imported doc.
   */
  endpoint_count: number;
  /**
   * Raw body size in bytes (state.OpenAPIImportMaxDocBytes = 256 KiB).
   */
  byte_size: number;
  /**
   * First-import timestamp; preserved across re-imports.
   */
  captured_at: string;
  /**
   * Most-recent write timestamp; bumped on every import.
   */
  updated_at: string;
};

