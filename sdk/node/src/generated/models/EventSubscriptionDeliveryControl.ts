/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventCircuitBreakerResponse } from './EventCircuitBreakerResponse.js';
/**
 * Live operator controls for a captured application subscription. Pending and processing counts include publication and historical backfill recipients retained in the backlog.
 */
export type EventSubscriptionDeliveryControl = {
  circuit_breaker?: EventCircuitBreakerResponse;
  subscription_id: string;
  app_id: string;
  paused: boolean;
  rate_per_second: number;
  pending_recipients: number;
  processing_recipients: number;
  oldest_pending_at?: string;
  oldest_age_seconds: number;
  /**
   * Absent until an operator sets a control.
   */
  updated_at?: string;
};

