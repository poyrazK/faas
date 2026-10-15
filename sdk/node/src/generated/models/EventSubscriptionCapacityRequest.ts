/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventSubscriptionCapacityGuard } from './EventSubscriptionCapacityGuard.js';
/**
 * Explicit per-subscription live-delivery ceiling. Zero restores the plan default; positive values must not exceed the current plan ceiling. Requires deploy write or admin scope and MFA.
 */
export type EventSubscriptionCapacityRequest = {
  /**
   * Optional nonzero command UUID scoped to this owned subscription. Repeat the same value and expectations while its 30-day receipt remains retained; repeats return the original receipt without applying again, even after later settings or plan changes. Different normalized inputs conflict. No guarantee after expiry or deletion.
   */
  command_id?: string;
  expected?: EventSubscriptionCapacityGuard;
  /**
   * Maximum pending plus dispatching deliveries; zero restores the current plan default.
   */
  max_pending_deliveries: number;
};

