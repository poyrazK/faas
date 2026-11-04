/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Cumulative rekey walk snapshot. Returned by
 * GET /v1/admin/secrets/rekey-progress and persisted to
 * FAAS_REKEY_PROGRESS_FILE (default
 * /var/lib/faas/rekey-progress.json) on every batch tick.
 * `last_id` is the (account_id, app_id, key) cursor the walk
 * will resume from on daemon restart.
 *
 */
export type RekeyProgress = {
  /**
   * Running count of rows observed so far. Grows as the walk paginates through (account_id, app_id, key) order.
   */
  total: number;
  /**
   * Rows successfully unsealed under the previous identity and re-sealed under the current one.
   */
  rekeyed: number;
  /**
   * Rows already sealed under the current identity (no-op). Seen-set dedupe — does NOT mean the row is unreadable.
   */
  skipped: number;
  /**
   * Rows where the unseal step threw. A non-zero value warrants operator action (toggle FAAS_REKEY_ENABLED and restart the API to retry).
   */
  failed: number;
  /**
   * Resume cursor in (account_id|app_id|key) form. Empty when the walk has just started or finished a clean sweep.
   */
  last_id?: string;
};

