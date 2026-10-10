/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryNotificationRetryTarget } from './EventRecoveryNotificationRetryTarget.js';
/**
 * Explicit retry intent with a canonical nonzero UUID request identity. Target order is ignored for idempotency; target fields are immutable under this identity.
 */
export type EventRecoveryNotificationRetryRequest = {
  request_id: string;
  targets: Array<EventRecoveryNotificationRetryTarget>;
};

