/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for POST /v1/apps/{slug}/alert-presets/{name}/enable.
 * The (name, metric, comparison, threshold, window_spec,
 * default_cooldown_minutes) sextuple is pre-filled from the
 * catalog; the caller supplies only the delivery-side fields.
 *
 */
export type EnableAlertPresetRequest = {
  webhook_url: string;
  webhook_secret: string;
  /**
   * Override for the preset's default_cooldown_minutes.
   * Omit to use the catalog default.
   *
   */
  cooldown_minutes?: number;
  /**
   * Whether the instantiated rule is enabled. Defaults to
   * true; pass false to stage the rule in disabled state.
   *
   */
  enabled?: boolean;
};

