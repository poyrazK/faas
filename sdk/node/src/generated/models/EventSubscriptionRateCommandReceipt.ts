/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventSubscriptionRateLimit } from './EventSubscriptionRateLimit.js';
/**
 * Historical idempotent command outcome; rejected commands preserve configuration. Look up after an uncertain mutation before submitting a new UUID.
 */
export type EventSubscriptionRateCommandReceipt = {
  command_id: string;
  app_id: string;
  subscription_id: string;
  sequence: number;
  outcome: 'applied' | 'rejected';
  result: EventSubscriptionRateLimit;
  recorded_at: string;
  retain_until: string;
};

