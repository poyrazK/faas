/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryNotificationRetryTarget } from './EventRecoveryNotificationRetryTarget.js';
/**
 * Immutable original receiver decision alongside current retained delivery status. An unavailable current row does not rewrite or invalidate the original result.
 */
export type EventRecoveryNotificationRetryDecision = {
  target: EventRecoveryNotificationRetryTarget;
  state: 'queued' | 'skipped';
  reason?: string;
  replay_generation?: number;
  current_delivery_status: string;
  current_replay_generation?: number;
};

