/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Preview or original committed repair response. Requires usage recovery describes the post-repair state; receipt replay is not a live admission or coverage read. Use accounting diagnostics for current status.
 */
export type ManagedPostgresAccountingReconciliationResult = {
  /**
   * Identity of the previewed or committed repair receipt.
   */
  reconciliation_id: string;
  /**
   * Reconciled catalog database.
   */
  database_id: string;
  /**
   * Revision fencing the reviewed request and relevant catalog/ledger/coverage state.
   */
  revision: string;
  /**
   * True for a committed repair or durable replay.
   */
  applied: boolean;
  /**
   * Previous logical deletion timestamp retained in the audit; it did not prove shutdown.
   */
  previous_deleted_at?: string;
  /**
   * Attested actual shutdown boundary.
   */
  shutdown_at: string;
  /**
   * Preserved evidence observation time.
   */
  observed_at: string;
  /**
   * Restore descendant still shares its existing accounting root.
   */
  shared_accounting: boolean;
  /**
   * Coverage resets on apply; recovery and final correction evidence must satisfy admission.
   */
  requires_usage_recovery: boolean;
};

