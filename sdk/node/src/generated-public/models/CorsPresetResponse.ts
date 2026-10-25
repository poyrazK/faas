/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One cors_presets row (issue #975 #4 PR-B / ADR-129). The
 * id is a server-generated UUID; app_id is null on the
 * wire for account-wide presets and a UUID for app-scoped
 * presets (the SQL NULL marker is the canonical "account-
 * wide" encoding; the empty string is not used). The
 * allow_origins array accepts the same CorsOriginPattern
 * grammar as the inline EdgeRuleCORSAction field. The
 * create-time gate enforces AllowCredentials: true +
 * AllowOrigins: ["*"] ⇒ 422 (ADR-091 D12). Updated_at is
 * bumped on every successful PATCH; the gateway's
 * per-account overlay cache invalidates on pg_notify
 * (cors_preset_changed).
 *
 */
export type CorsPresetResponse = {
  id: string;
  account_id: string;
  /**
   * Optional app scoping (issue #975 #4 PR-B). Null on the
   * wire = account-wide preset (visible to every app on
   * the account). A UUID = app-scoped preset (visible
   * only to that one app; cross-tenant IDOR collapses to
   * 404).
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
  created_at: string;
  updated_at: string;
};

