/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Result of an operator-initiated Polar refund. The local invoice and
 * provider refund identifiers are both returned for reconciliation.
 * Amounts are integer EUR cents.
 *
 */
export type AdminRefundResponse = {
  account_id: string;
  invoice_id: string;
  provider: 'polar';
  provider_refund_id: string;
  /**
   * Polar order ID used by the refund.
   */
  charge_id: string;
  amount_cents: number;
  currency: string;
  /**
   * Provider refund status.
   */
  status: string;
};

