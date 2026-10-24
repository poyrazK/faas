/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for POST /v1/cors-presets. The customer must supply
 * at least one allow_origin and one allow_method. AppID is
 * optional on the wire (null pointer = "account-wide",
 * non-nil = "app-scoped"); the handler maps the
 * pointer-nil case to a SQL NULL on insert. Name length
 * is 1..64 characters (cors_presets_name_check). The
 * *+credentials footgun (ADR-091 D12) is enforced at
 * validate-time.
 *
 */
export type CreateCorsPresetRequest = {
  /**
   * Optional app scoping. Null = account-wide. Set to a
   * UUID = app-scoped.
   *
   */
  app_id?: string | null;
  name: string;
  description?: string;
  allow_origins: Array<string>;
  allow_methods: Array<string>;
  allow_headers?: Array<string>;
  expose_headers?: Array<string>;
  allow_credentials: boolean;
  max_age_seconds: number;
};

