/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryNotificationRetryTarget } from './EventRecoveryNotificationRetryTarget.js';
/**
 * Frozen decision for an explicitly requested receiver. Queued starts a new delivery generation; skipped preserves delivery state and explains the reason.
 */
export type EventRecoveryNotificationRetryResult = {
  target: EventRecoveryNotificationRetryTarget;
  state: 'queued' | 'skipped';
  reason?: string;
  replay_generation?: number;
};

