/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventCircuitBreakerResponse } from './EventCircuitBreakerResponse.js';
import type { EventSubscriptionCapacityCommandBudget } from './EventSubscriptionCapacityCommandBudget.js';
import type { EventSubscriptionCapacityCommandReceipt } from './EventSubscriptionCapacityCommandReceipt.js';
import type { EventSubscriptionRateLimit } from './EventSubscriptionRateLimit.js';
/**
 * Live operator controls for a captured application subscription. Pending and processing counts include publication and historical backfill recipients retained in the backlog.
 */
export type EventSubscriptionDeliveryControl = {
  /**
   * Optional token-bucket observation; absent on older responses.
   */
  rate_limit?: EventSubscriptionRateLimit;
  /**
   * Optional complete receipt budget in read-only delivery-control status. Mutation responses may omit it; absence means unknown.
   */
  capacity_command_budget?: EventSubscriptionCapacityCommandBudget;
  capacity_command_receipt?: EventSubscriptionCapacityCommandReceipt;
  /**
   * Configured live-delivery ceiling; zero uses the current plan limit.
   */
  max_pending_deliveries: number;
  /**
   * Effective ceiling after applying the current plan limit.
   */
  effective_max_pending_deliveries: number;
  /**
   * Current pending plus dispatching invocations with trusted delivery slots; excludes untagged legacy work.
   */
  live_deliveries: number;
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
   * Updated at in the event subscription delivery control: absent until an operator sets a control.
   */
  updated_at?: string;
};

