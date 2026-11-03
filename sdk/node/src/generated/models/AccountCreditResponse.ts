/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One row in the account_credits table. cents_remaining
 * is the integer-cents balance still available; consumption
 * decrements it (the consumption reducer lands in a follow-up
 * PR). expires_at is RFC 3339 when set; absent (or null) means
 * the credit is valid until fully consumed. reason is the
 * operator-supplied audit text (3..500 chars).
 *
 */
export type AccountCreditResponse = {
  id: string;
  account_id: string;
  cents_remaining: number;
  reason: string;
  created_at: string;
  expires_at?: string | null;
};

