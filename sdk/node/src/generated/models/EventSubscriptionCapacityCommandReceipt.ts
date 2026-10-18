/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventSubscriptionCapacityGuard } from './EventSubscriptionCapacityGuard.js';
/**
 * Immutable acknowledgment of a successful capacity command. The surrounding control response reports current state; this receipt preserves the original outcome. Retained for 30 days without extension on repeat. No event payloads or credentials.
 */
export type EventSubscriptionCapacityCommandReceipt = {
  command_id: string;
  app_id: string;
  subscription_id: string;
  requested_max_pending_deliveries: number;
  expected?: EventSubscriptionCapacityGuard;
  previous_max_pending_deliveries: number;
  plan_max_pending_deliveries: number;
  effective_max_pending_deliveries: number;
  /**
   * Whether the original successful command changed the saved setting; both consume a receipt slot.
   */
  outcome: 'applied' | 'unchanged';
  recorded_at: string;
  retain_until: string;
};

