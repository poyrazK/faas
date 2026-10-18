/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsNotificationSignal } from './ObsNotificationSignal.js';
/**
 * Attempts and failures for a reviewed integration type across all receivers. Counter presence does not establish configuration; receiver names and addresses are omitted.
 */
export type ObsNotificationIntegration = {
  integration: 'email' | 'pushover' | 'slack' | 'webhook' | 'pagerduty' | 'opsgenie' | 'victorops' | 'wechat' | 'telegram' | 'discord' | 'webex' | 'sns' | 'msteams' | 'msteamsv2' | 'rocketchat' | 'jira';
  attempts: ObsNotificationSignal;
  failures: ObsNotificationSignal;
};

