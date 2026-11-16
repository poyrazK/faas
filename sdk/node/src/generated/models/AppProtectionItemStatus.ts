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
   * active when the object was created; pending while the app waits for its first healthy deploy.
   */
  state: 'active' | 'pending';
  /**
   * The created object's id, or a release gate's name. Present when state is active.
   */
  object_id?: string;
};

