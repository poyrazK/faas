/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Subscription execution-coverage status and durable progress of forward reconciliation and legacy repair.
 */
export type EventExecutionReconciliation = {
  /**
   * Cumulative retained receipt candidates examined by this request.
   */
  repair_scanned: number;
  /**
   * Newly associated authoritative roots; existing associations are not counted.
   */
  repair_repaired: number;
  /**
   * Examined candidates with missing or incompatible execution evidence; not a live blocker count.
   */
  repair_unresolved: number;
  /**
   * Examined candidates with competing acceptance or incompatible existing lineage; not a live blocker count.
   */
  repair_conflicts: number;
  subscription_id: string;
  app_id: string;
  status: 'not_requested' | 'waiting_for_lineage' | 'completed' | 'not_required';
  requested_at?: string;
  collection_started_at?: string;
  /**
   * A retained app invocation lacks authoritative ledger lineage; unrelated app invocations can conservatively block reconciliation.
   */
  unattributed_execution_present: boolean;
};

