/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for POST /v1/apps/{slug}/alert-presets/{name}/enable.
 * The (name, metric, comparison, threshold, window_spec,
 * default_cooldown_minutes) sextuple is pre-filled from the
 * catalog; the caller supplies the delivery-side fields and an
 * optional safe-release action.
 *
 */
export type EnableAlertPresetRequest = {
  /**
   * Signed webhook destination; required with webhook_secret unless channel_ids is set.
   */
  webhook_url?: string;
  /**
   * HMAC secret for the webhook; required with webhook_url unless channel_ids is set.
   */
  webhook_secret?: string;
  /**
   * Notification channels (ADR-749) the instantiated rule delivers to; with at least one, the webhook fields may be omitted.
   */
  channel_ids?: Array<string>;
  /**
   * Action to run when the instantiated alert fires. Omit to
   * use the default webhook-only behavior. The login_target_pressure
   * and login_target_signal_health presets support webhook only.
   *
   */
  action?: 'webhook' | 'rollback' | 'demote' | 'promote';
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

