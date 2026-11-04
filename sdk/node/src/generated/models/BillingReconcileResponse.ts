/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Returned by POST /v1/admin/billing-reconcile/{id}.
 * mb_seconds is the integer total the provider SDK returned
 * for the rolling 30-day window [start, end). Operators can
 * diff the SDK-side number against the local usage_minutes
 * sum to spot drift between the platform's billing source
 * of truth and the provider's ledger.
 *
 */
export type BillingReconcileResponse = {
  account_id: string;
  /**
   * Window start (RFC 3339, UTC).
   */
  start: string;
  /**
   * Window end (RFC 3339, UTC).
   */
  end: string;
  /**
   * Integer mb_seconds total for the window.
   */
  mb_seconds: number;
};

