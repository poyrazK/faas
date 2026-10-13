/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type EventExecutionReconciliation = {
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

