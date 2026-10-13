/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventConsumerCapacityScope } from './EventConsumerCapacityScope.js';
import type { EventSubscriptionCapacityCommandBudget } from './EventSubscriptionCapacityCommandBudget.js';
/**
 * Read-only current live slot usage and effective capacity waiting reasons from one owned subscription snapshot. Independent of the other delivery-health reads and history window. Recorded waits may remain after capacity drains. Age is since event acceptance, not continuous capacity-wait duration. No capacity reservation or delivery-order guarantee.
 */
export type EventConsumerCapacityPressure = {
  /**
   * Optional receipt budget from the same snapshot and observed_at as this pressure. Absent on older servers; absence does not mean slots are available.
   */
  capacity_command_budget?: EventSubscriptionCapacityCommandBudget;
  /**
   * Current plan consumer ceiling before applying the saved subscription setting; used to validate proposed capacity changes.
   */
  plan_max_pending_deliveries: number;
  app_id: string;
  subscription_id: string;
  observed_at: string;
  coverage: 'live_slots_and_all_pending_application_recipients';
  /**
   * All trusted live slots and retained pending application recipients in this scope were assessed; not inferred from a bounded sample.
   */
  complete: boolean;
  /**
   * Current first full admission ceiling in consumer then app then account order; empty means none observed. Not inferred from recorded waits.
   */
  blocking_scope: '' | 'consumer' | 'app' | 'account';
  /**
   * Saved subscription ceiling; zero uses the current plan default.
   */
  configured_max_pending_deliveries: number;
  /**
   * Consumer, app and account scopes in admission priority order. Waiting counts refer only to this subscription; live app/account counts include sibling deliveries in those scopes.
   */
  scopes: Array<EventConsumerCapacityScope>;
};

