/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Spend-cap payload (issue #561). *int64 so a missing/null
 * field round-trips as NULL (no cap). 0 is a valid write and
 * means "no overage allowed". Negative values are rejected at
 * the validator before reaching the store (the migration CHECK
 * at accounts/00049 is the storage-layer enforcement).
 *
 */
export type RaiseOverageCapRequest = {
  /**
   * Cents-per-month ceiling, or null to clear.
   */
  overage_cap_cents: (number | null);
};

