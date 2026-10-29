/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryPollingNotification } from './WorkflowRecoveryPollingNotification.js';
/**
 * Delivery observations for frozen receipt receivers. Missing deliveries without an outbox entry are unknown. No receivers is not delivered.
 */
export type WorkflowRecoveryOverviewAlert = {
  notification: WorkflowRecoveryPollingNotification;
  delivery_status: 'no_receivers' | 'relay_pending' | 'pending' | 'delivered' | 'failed' | 'unknown';
  expected: number;
  pending: number;
  in_flight: number;
  succeeded: number;
  failed: number;
  dead: number;
  missing: number;
  relay_pending: boolean;
};

