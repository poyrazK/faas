/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Wire shape for GET /v1/admin/billing-paddle-overage/preflight
 * Operator-side guard that
 * probes information_schema.columns for the four migration-00041
 * columns + counts the per-state rows in a single round-trip.
 *
 * table_exists is false when the paddle_overage_dedupe table is
 * entirely absent (migrations 00034 + 00041 both unapplied).
 * has_window_start / has_state / has_claimed_at / has_claimed_by
 * each correspond to one of the four columns added by
 * migration 00041; all four must be true for the metering service
 * overage pusher to land. pending_rows / completed_rows are
 * the per-state row totals (a single SQL filter pair).
 *
 */
export type BillingPaddleOveragePreflightResponse = {
  /**
   * true if the paddle_overage_dedupe table is present (00034+).
   */
  table_exists: boolean;
  /**
   * true if the migration-00041 window_start column exists.
   */
  has_window_start: boolean;
  /**
   * true if the migration-00041 state column exists.
   */
  has_state: boolean;
  /**
   * true if the migration-00041 claimed_at column exists.
   */
  has_claimed_at: boolean;
  /**
   * true if the migration-00041 claimed_by column exists.
   */
  has_claimed_by: boolean;
  /**
   * count(*) where state = pending.
   */
  pending_rows: number;
  /**
   * count(*) where state = completed.
   */
  completed_rows: number;
};

