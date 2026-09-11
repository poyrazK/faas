/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { GithubCheckUpdateRecord } from './GithubCheckUpdateRecord.js';
import type { GithubWebhookDeliveryRecord } from './GithubWebhookDeliveryRecord.js';
/**
 * Operator-safe projections of githubd's durable recovery queues. Webhook payloads are never included.
 */
export type GithubRecoveryStatusResponse = {
  deliveries: Array<GithubWebhookDeliveryRecord>;
  check_updates: Array<GithubCheckUpdateRecord>;
};

