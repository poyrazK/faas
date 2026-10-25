/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for PATCH /v1/cors-presets/{id}. Every field is
 * optional (PATCH nil-skip convention). At least one field
 * must be present (an empty PATCH is rejected with 422
 * cors_preset_update_requires_field). The same partial
 * grammar check that fires on CreateCorsPresetRequest
 * (CorsOriginPattern, *+credentials footgun) fires here
 * on the partial payload; the apid handler additionally
 * re-validates against the merged post-update shape so a
 * PATCH that flips AllowCredentials to true while leaving
 * AllowOrigins=["*"] is rejected.
 *
 * app_id uses the **string tri-state: outer null = "do
 * not touch", inner null = "set to NULL (account-wide)",
 * inner non-null = "set to UUID (app-scoped)".
 *
 */
export type UpdateCorsPresetRequest = {
  /**
   * Optional app scoping. Outer null = do not touch.
   * Inner null = set to NULL (account-wide). Inner
   * non-null = set to UUID (app-scoped).
   *
   */
  app_id?: string | null;
  name?: string;
  description?: string;
  allow_origins?: Array<string>;
  allow_methods?: Array<string>;
  allow_headers?: Array<string>;
  expose_headers?: Array<string>;
  allow_credentials?: boolean;
  max_age_seconds?: number;
};

