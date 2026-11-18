/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One production protection object.
 */
export type AppProtectionItemStatus = {
  item: 'availability_slo' | 'latency_slo' | 'health_check' | 'email_channel' | 'availability_burn_alert' | 'latency_burn_alert' | 'health_alert' | 'availability_gate';
  kind: 'slo' | 'synthetic_check' | 'notification_channel' | 'alert_rule' | 'release_gate';
  name: string;
  /**
   * active when the object was created and still exists; pending while the app waits for a live production deployment; skipped when it could not be created yet (see reason), retried automatically; removed when you deleted it, in which case it is not created again.
   */
  state: 'active' | 'pending' | 'skipped' | 'removed';
  /**
   * The created object's id. Present when state is active or removed.
   */
  object_id?: string;
  /**
   * Why a skipped object could not be created yet: the plan does not include it, the feature is not available to the account, the account email is not verified, you already have an object with this name, the app or account is at its limit, the SLO, email channel or health check it needs was not created, or free health probes are not running on this platform.
   */
  reason?: 'plan' | 'not_released' | 'email_unverified' | 'name_taken' | 'limit_reached' | 'needs_slo' | 'needs_channel' | 'needs_check' | 'probes_unavailable';
};

