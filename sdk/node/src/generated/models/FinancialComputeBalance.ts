/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Conservative app admission ledger before allowances, including startup and rounding per funded minute. These commitments may exceed invoice compute charges. Expired funding stays reserved until physical retirement and settlement. Other meters, subscriptions and taxes are excluded.
 */
export type FinancialComputeBalance = {
  initialized: boolean;
  app_id: string;
  period_start: string;
  period_end: string;
  committed_millicents: number;
  reserved_millicents: number;
  remaining_millicents: number;
  unconfirmed_leases: number;
  held: boolean;
  observed_at: string;
};

