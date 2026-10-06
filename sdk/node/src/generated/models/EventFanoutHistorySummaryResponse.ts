/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable recipient counters and detail coverage for the retained receipt.
 */
export type EventFanoutHistorySummaryResponse = {
  subscription_id: string;
  /**
   * Recorded routing and replay observations including coalesced waits.
   */
  observed_outcomes: number;
  /**
   * Highest durable cumulative recipient deferral checkpoint.
   */
  capacity_deferrals: number;
  /**
   * Repeated waits represented by counters instead of detail rows.
   */
  coalesced_outcomes: number;
  /**
   * Detail rows removed by retention or budgets.
   */
  compacted_outcomes: number;
  /**
   * Highest removed detail ID; protected older rows can remain, so this is not a contiguous missing prefix.
   */
  compacted_through_id?: number;
  /**
   * Latest occurrence time among removed detail rows.
   */
  compacted_through_at?: string;
  /**
   * First recorded capacity wait after summary rollout.
   */
  first_capacity_wait_at?: string;
  last_capacity_wait_at?: string;
  /**
   * Most recent recorded capacity scope, also retained after recovery.
   */
  last_capacity_scope?: 'consumer' | 'app' | 'account';
  retained_records: number;
  /**
   * Logical detail bytes including a fixed per-row allowance; excludes physical storage and indexes.
   */
  retained_bytes: number;
};

