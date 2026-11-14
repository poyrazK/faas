/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsNotificationSignal } from './ObsNotificationSignal.js';
/**
 * Completed transport request observations for one reviewed receiver role
 * and integration type. Multiple destinations within the group aggregate.
 * Signal healthy describes usable evidence, not human acknowledgement.
 * Expected is null when independent deployment intent cannot be established.
 *
 */
export type ObsNotificationReceiver = {
  receiver: 'faas-heartbeat' | 'faas-page' | 'faas-warn';
  integration: 'email' | 'pushover' | 'slack' | 'webhook' | 'pagerduty' | 'opsgenie' | 'victorops' | 'wechat' | 'telegram' | 'discord' | 'webex' | 'sns' | 'msteams' | 'msteamsv2' | 'rocketchat' | 'jira';
  expected: boolean | null;
  state: 'healthy' | 'partial' | 'degraded';
  reason?: string;
  /**
   * Completed transport request increase over ten minutes, including failed requests and retry attempts. Insufficient samples are null.
   */
  requests: ObsNotificationSignal;
  /**
   * Failed completed request increase over ten minutes, including retries. Fresh zero is distinct from missing evidence.
   */
  failures: ObsNotificationSignal;
  /**
   * Value is Unix seconds when two collector observations confirmed a new successful completed request count. Observed_at is the freshness of that gauge, not the delivery time. A cold baseline is no_samples with a null value, never an epoch-zero receipt. Older confirmed history may remain valid with a freshly read gauge. It does not establish per-episode delivery or human or external-watchdog receipt.
   */
  last_success_observation: ObsNotificationSignal;
};

