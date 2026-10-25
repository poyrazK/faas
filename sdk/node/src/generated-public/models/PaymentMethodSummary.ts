/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Card-on-file summary (issue #242). Provider-agnostic shape —
 * the same wire shape is returned whether the operator runs
 * Stripe or Paddle. brand is the lowercase network label
 * ("visa", "mastercard", "amex"); last4 is the last-4 of the
 * PAN (no full PAN, no PCI surface); exp_month / exp_year are
 * integer card-face expiry fields.
 *
 */
export type PaymentMethodSummary = {
  /**
   * Card network (lowercase).
   */
  brand: string;
  /**
   * Last 4 digits of the PAN.
   */
  last4: string;
  /**
   * Card expiry month (1-12).
   */
  exp_month: number;
  /**
   * Card expiry year.
   */
  exp_year: number;
};

